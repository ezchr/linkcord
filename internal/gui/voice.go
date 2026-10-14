package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/ningen/v3"
	"github.com/ezchr/linkcord/internal/dcsound"
	"github.com/ezchr/linkcord/internal/golive"
	"github.com/ezchr/linkcord/internal/hotkey"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconMic      = mustIcon(icons.AVMic)
	iconMicOff   = mustIcon(icons.AVMicOff)
	iconHeadset  = mustIcon(icons.HardwareHeadset)
	iconDeafened = mustIcon(icons.AVVolumeOff)
	iconHangUp   = mustIcon(icons.CommunicationCallEnd)
	iconSignal   = mustIcon(icons.DeviceSignalCellular4Bar)
)

// voiceSensitivity is how loud, in dB, the microphone must be for what it
// captures to be sent; it is discordo's default.
const voiceSensitivity = -40

// voiceState is the voice call of the window.
type voiceState struct {
	// session talks to the voice channel joined. It is made on the first
	// join, for the state it was made with.
	session *voice.Session
	st      *ningen.State
	// talker is session, for the key to talk to reach from the goroutine
	// that watches keys, and keys watches the keybinds.
	talker atomic.Pointer[voice.Session]
	keys   *hotkey.Watcher

	// sounds are Discord's sounds, played by effects, made on first use;
	// viewersHeard is how many watched the stream when last looked at, to
	// hear who starts and stops.
	sounds       *dcsound.Library
	effects      *voice.Effects
	viewersHeard int

	// muted and deafened are what the user chose with the buttons, which also
	// apply to the next call joined.
	muted, deafened bool

	// settings are how loud each user is played and the devices chosen,
	// kept between runs once loaded.
	settings       voiceSettings
	settingsLoaded bool
	// inputs and outputs are the microphones and speakers to choose from.
	inputs, outputs []voice.Device

	// demoChannel is the voice channel joined in the demo, which has no
	// session.
	demoChannel discord.ChannelID
	// demoSpeaker is who always speaks in the demo, where the user speaks
	// while in a voice channel and not muted.
	demoSpeaker discord.UserID

	// stream is the screen shared, if any, streamStarting reports that it is
	// starting, and picker is the window to choose it. demoStream is the
	// picture of what the demo pretends to share.
	stream         *golive.Stream
	streamStarting bool
	picker         streamPicker
	demoStream     *image.RGBA
	// previewImg is the last picture of what is shared that was shown.
	previewImg   *image.RGBA
	previewImgOp paint.ImageOp

	// err says why the last voice action failed, or what it did if info,
	// until errUntil.
	err      string
	info     bool
	errUntil time.Time

	muteBtn, deafBtn, leaveBtn, settingsBtn              widget.Clickable
	channelBtn, camBtn, screenBtn, activityBtn, soundBtn widget.Clickable
}

// voiceSettings are what the user chose for voice channels, saved in
// voice.json beside the log.
type voiceSettings struct {
	// Volumes are how loud users are played, from 0 to 2, for those not
	// played as they are sent.
	Volumes map[discord.UserID]float32 `json:"volumes,omitempty"`
	// Muted are the users silenced for the user only.
	Muted map[discord.UserID]bool `json:"muted,omitempty"`
	// Input and Output are the IDs of the microphone and speakers chosen,
	// "" for the default ones.
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	// StreamHeight and StreamFPS are the quality chosen last to stream at.
	StreamHeight int `json:"stream_height,omitempty"`
	StreamFPS    int `json:"stream_fps,omitempty"`
	// StreamMute streams without sound.
	StreamMute bool `json:"stream_mute,omitempty"`
	// InputVolume and OutputVolume are how many times as loud the
	// microphone is sent and calls are played, from 0 to 2; nil is 1.
	InputVolume  *float32 `json:"input_volume,omitempty"`
	OutputVolume *float32 `json:"output_volume,omitempty"`
	// Sensitivity is how loud, in dB, the microphone must be for what it
	// captures to be sent; nil is voiceSensitivity.
	Sensitivity *float64 `json:"sensitivity,omitempty"`
	// PushToTalk sends what the microphone captures only while the key of
	// push_to_talk is held.
	PushToTalk bool `json:"push_to_talk,omitempty"`
	// NoNoiseSuppression and NoAutoGain turn off the noise suppression and
	// the automatic gain control of the microphone, on by default as in
	// Discord.
	NoNoiseSuppression bool `json:"no_noise_suppression,omitempty"`
	NoAutoGain         bool `json:"no_auto_gain,omitempty"`
	// NoSounds turns off Discord's sounds, such as of muting.
	NoSounds bool `json:"no_sounds,omitempty"`
	// Keybinds are the keys of each action of keybindActions, which act in
	// any window.
	Keybinds map[string]hotkey.Combo `json:"keybinds,omitempty"`
}

// inputVolume is how many times as loud the microphone is sent.
func (s *voiceSettings) inputVolume() float32 {
	if s.InputVolume == nil {
		return 1
	}
	return *s.InputVolume
}

// outputVolume is how many times as loud calls are played.
func (s *voiceSettings) outputVolume() float32 {
	if s.OutputVolume == nil {
		return 1
	}
	return *s.OutputVolume
}

// sensitivity is how loud, in dB, the microphone must be to be sent.
func (s *voiceSettings) sensitivity() float64 {
	if s.Sensitivity == nil {
		return voiceSensitivity
	}
	return *s.Sensitivity
}

// applyVoiceSettings has the call and the microphone test follow the
// volumes, sensitivity and input mode chosen.
func (u *ui) applyVoiceSettings() {
	s := u.voiceSettings()
	if vs := u.voice.session; vs != nil {
		vs.SetInputVolume(s.inputVolume())
		vs.SetOutputVolume(s.outputVolume())
		vs.SetSensitivity(s.sensitivity())
		vs.SetPushToTalk(s.PushToTalk)
		vs.SetNoiseSuppression(!s.NoNoiseSuppression)
		vs.SetAutoGain(!s.NoAutoGain)
	}
	if c := u.chat; c != nil && c.settings.test != nil {
		c.settings.test.SetInputVolume(s.inputVolume())
		c.settings.test.SetOutputVolume(s.outputVolume())
		c.settings.test.SetNoiseSuppression(!s.NoNoiseSuppression)
		c.settings.test.SetAutoGain(!s.NoAutoGain)
	}
}

// keybindActions are what keybinds can do, in the order the settings list
// them.
var keybindActions = []struct{ id, name, about string }{
	{"push_to_talk", "Push to Talk", "Hold to talk, when the input mode is Push to Talk."},
	{"toggle_mute", "Toggle Mute", "Mutes or unmutes your microphone."},
	{"toggle_deafen", "Toggle Deafen", "Deafens or undeafens you."},
}

// applyKeybinds watches the keys chosen for each action, from any window,
// even while Linkcord's is hidden.
func (u *ui) applyKeybinds() {
	v := &u.voice
	if v.keys == nil {
		v.keys = hotkey.Start()
	}
	toggle := func(deafen bool) func() {
		return func() {
			u.post(func() {
				u.toggleVoice(deafen)
				u.syncDemoVoice()
			})
		}
	}
	talk := func(on bool) func() {
		return func() {
			if s := v.talker.Load(); s != nil {
				s.SetTalking(on)
			}
			u.post(func() {
				if u.voiceSettings().PushToTalk && u.voiceStatus().Joined && !v.muted {
					if on {
						u.playSound(dcsound.PTTStart)
					} else {
						u.playSound(dcsound.PTTStop)
					}
				}
			})
		}
	}
	var binds []hotkey.Bind
	for id, keys := range u.voiceSettings().Keybinds {
		b := hotkey.Bind{Keys: keys}
		switch id {
		case "push_to_talk":
			b.Down, b.Up = talk(true), talk(false)
		case "toggle_mute":
			b.Down = toggle(false)
		case "toggle_deafen":
			b.Down = toggle(true)
		default:
			continue
		}
		binds = append(binds, b)
	}
	v.keys.Set(binds)
}

func voiceSettingsPath() string {
	return filepath.Join(filepath.Dir(logPath()), "voice.json")
}

// voiceSettings returns the settings, read from the disk the first time.
func (u *ui) voiceSettings() *voiceSettings {
	v := &u.voice
	if !v.settingsLoaded {
		v.settingsLoaded = true
		if data, err := os.ReadFile(voiceSettingsPath()); err == nil {
			if err := json.Unmarshal(data, &v.settings); err != nil {
				slog.Warn("could not read the voice settings", "err", err)
			}
		}
		if v.settings.Volumes == nil {
			v.settings.Volumes = make(map[discord.UserID]float32)
		}
		if v.settings.Muted == nil {
			v.settings.Muted = make(map[discord.UserID]bool)
		}
	}
	return &v.settings
}

// saveVoiceSettings writes the settings to the disk.
func (u *ui) saveVoiceSettings() {
	// What is chosen in the demo is not kept over the user's settings.
	if c := u.chat; c != nil && c.demo {
		return
	}
	data, err := json.Marshal(u.voiceSettings())
	if err != nil {
		return
	}
	path := voiceSettingsPath()
	go func() {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			err = os.WriteFile(path, data, 0o600)
		}
		if err != nil {
			slog.Warn("could not save the voice settings", "err", err)
		}
	}()
}

// userVolume is how loud the user is played, from 0 to 2.
func (u *ui) userVolume(id discord.UserID) float32 {
	if vol, ok := u.voiceSettings().Volumes[id]; ok {
		return vol
	}
	return 1
}

// setUserVolume plays the user vol times as loud as they are sent. Close to
// 1, it snaps to 1, as Discord's slider does.
func (u *ui) setUserVolume(id discord.UserID, vol float32) {
	vol = min(max(vol, 0), 2)
	if vol > 0.96 && vol < 1.04 {
		vol = 1
	}
	s := u.voiceSettings()
	if vol == 1 {
		delete(s.Volumes, id)
	} else {
		s.Volumes[id] = vol
	}
	u.applyVolume(id)
}

// toggleLocalMute silences the user for the user only, or stops.
func (u *ui) toggleLocalMute(id discord.UserID) {
	s := u.voiceSettings()
	if s.Muted[id] {
		delete(s.Muted, id)
	} else {
		s.Muted[id] = true
	}
	u.applyVolume(id)
	u.saveVoiceSettings()
}

// applyVolume plays the user as loud as chosen.
func (u *ui) applyVolume(id discord.UserID) {
	s := u.voice.session
	if s == nil {
		return
	}
	vol := u.userVolume(id)
	if u.voiceSettings().Muted[id] {
		vol = 0
	}
	s.SetVolume(id, vol)
}

// loadDevices lists the microphones and speakers to choose from.
func (u *ui) loadDevices() {
	inputs, outputs, err := voice.Devices()
	if err != nil {
		slog.Warn("could not list the audio devices", "err", err)
		return
	}
	u.voice.inputs, u.voice.outputs = inputs, outputs
}

// setDevices captures the microphone with the ID input and plays on the
// speakers with the ID output, "" being the default ones.
func (u *ui) setDevices(input, output string) {
	s := u.voiceSettings()
	s.Input, s.Output = input, output
	u.saveVoiceSettings()
	if vs := u.voice.session; vs != nil {
		go func() {
			if err := vs.SetDevices(input, output); err != nil {
				u.post(func() { u.voiceFailed("Could not change the audio device", err) })
			}
		}()
	}
}

// voiceStatus is the voice channel joined or being joined, and whether it
// is joined.
func (u *ui) voiceStatus() voice.Status {
	v := &u.voice
	if v.demoChannel.IsValid() {
		return voice.Status{ChannelID: v.demoChannel, Joined: true}
	}
	if v.session == nil {
		return voice.Status{}
	}
	return v.session.Status()
}

func (u *ui) voiceChannel() discord.ChannelID { return u.voiceStatus().ChannelID }

func (u *ui) voiceSpeaking(id discord.UserID) bool {
	if v := &u.voice; v.demoSpeaker.IsValid() {
		if c := u.chat; c != nil && id == c.me.ID {
			return v.demoChannel.IsValid() && !v.muted
		}
		return id == v.demoSpeaker
	}
	if s := u.voice.session; s != nil {
		return s.Speaking(id)
	}
	return false
}

// voiceFailed shows why a voice action failed for a while.
func (u *ui) voiceFailed(what string, err error) {
	slog.Warn("voice action failed", "what", what, "err", err)
	u.showVoiceMessage(what+": "+err.Error(), false, 8*time.Second)
}

// voiceInfo shows what a voice action did for a while.
func (u *ui) voiceInfo(msg string) {
	u.showVoiceMessage(msg, true, 4*time.Second)
}

func (u *ui) showVoiceMessage(msg string, info bool, d time.Duration) {
	u.voice.err, u.voice.info = msg, info
	u.voice.errUntil = time.Now().Add(d)
	time.AfterFunc(d+100*time.Millisecond, u.win.Invalidate)
}

// voiceMessage is what the last voice action said, while it shows, and
// whether it failed.
func (u *ui) voiceMessage() (msg string, failed bool) {
	v := &u.voice
	if v.err == "" || !time.Now().Before(v.errUntil) {
		return "", false
	}
	return v.err, !v.info
}

// voiceSession returns the session for st, made on first use.
func (u *ui) voiceSession(st *ningen.State) *voice.Session {
	v := &u.voice
	if v.session != nil && v.st == st {
		return v.session
	}
	settings := u.voiceSettings()
	v.session, v.st = voice.New(st.State, settings.sensitivity()), st
	v.talker.Store(v.session)
	u.applyVoiceSettings()
	if err := v.session.SetDevices(settings.Input, settings.Output); err != nil {
		slog.Warn("could not choose the audio devices", "err", err)
	}
	for id := range settings.Volumes {
		u.applyVolume(id)
	}
	for id := range settings.Muted {
		u.applyVolume(id)
	}
	updates := v.session.Updates()
	go func() {
		// The status and who speaks are read while laying out.
		for range updates {
			u.win.Invalidate()
		}
	}()
	return v.session
}

// joinable reports why the user cannot join ch, if they cannot.
func (c *chatView) joinable(ch discord.Channel) error {
	if c.demo || !ch.GuildID.IsValid() {
		return nil
	}
	if !c.can(ch.ID, discord.PermissionConnect) {
		return errors.New("you do not have permission to connect")
	}
	if ch.VoiceUserLimit > 0 && !c.can(ch.ID, discord.PermissionMoveMembers) {
		states, _ := c.st.Cabinet.VoiceStates(ch.GuildID)
		n := 0
		for _, vs := range states {
			if vs.ChannelID == ch.ID {
				n++
			}
		}
		// Discord does not answer a request to join a full channel.
		if n >= int(ch.VoiceUserLimit) {
			return errors.New("the channel is full")
		}
	}
	return nil
}

// joinVoice joins the voice channel ch, leaving the one the user is in.
func (c *chatView) joinVoice(ch discord.Channel) {
	u := c.u
	if u.voiceChannel() == ch.ID {
		return
	}
	if err := c.joinable(ch); err != nil {
		u.voiceFailed("Could not join "+ch.Name, err)
		return
	}
	u.stopStream()
	if c.demo {
		c.demoVoice(ch)
		return
	}
	s := u.voiceSession(c.st)
	muted, deafened := u.voice.muted, u.voice.deafened
	canSpeak := !ch.GuildID.IsValid() || c.can(ch.ID, discord.PermissionSpeak|discord.PermissionUseVAD)
	go func() {
		// Joining leaves the user muted; the buttons' choice applies after.
		err := s.Join(ch.ID)
		if err == nil && !muted && canSpeak {
			err = s.Toggle(false)
		}
		if err == nil && deafened {
			err = s.Toggle(true)
		}
		if err != nil {
			u.post(func() { u.voiceFailed("Could not join "+ch.Name, err) })
			return
		}
		u.post(func() {
			u.playSound(dcsound.UserJoin)
			if c := u.chat; c != nil {
				c.trackCallers()
			}
		})
	}()
}

// leaveVoice leaves the voice channel the user is in, if any.
func (u *ui) leaveVoice() {
	if u.voiceChannel().IsValid() {
		u.playSound(dcsound.Disconnect)
	}
	u.stopStream()
	// A stream is watched from the voice channel it is in.
	if u.chat != nil {
		u.chat.stopWatching()
	}
	v := &u.voice
	if v.demoChannel.IsValid() && u.chat != nil {
		u.chat.demoVoice(discord.Channel{})
		return
	}
	if s := v.session; s != nil {
		go func() {
			if err := s.Leave(); err != nil {
				u.post(func() { u.voiceFailed("Could not leave the voice channel", err) })
			}
		}()
	}
}

// toggleVoice mutes or unmutes the user, or deafens or undeafens them if
// deafen, the way Discord does: deafening also mutes, and unmuting also
// undeafens.
func (u *ui) toggleVoice(deafen bool) {
	v := &u.voice
	if deafen {
		v.deafened = !v.deafened
		v.muted = v.muted || v.deafened
	} else {
		v.muted = !v.muted
		if !v.muted {
			v.deafened = false
		}
	}
	switch {
	case deafen && v.deafened:
		u.playSound(dcsound.Deafen)
	case deafen:
		u.playSound(dcsound.Undeafen)
	case v.muted:
		u.playSound(dcsound.Mute)
	default:
		u.playSound(dcsound.Unmute)
	}
	if s := v.session; s != nil && s.Status().Joined {
		go func() {
			if err := s.Toggle(deafen); err != nil {
				u.post(func() { u.voiceFailed("Could not change the microphone or audio settings", err) })
			}
		}()
	}
}

// demoVoice puts the user in ch in the demo, or in no voice channel if ch is
// the zero channel, as Discord would report it.
func (c *chatView) demoVoice(ch discord.Channel) {
	u := c.u
	if old := u.voice.demoChannel; old.IsValid() {
		if och, err := c.st.Cabinet.Channel(old); err == nil {
			c.st.Cabinet.VoiceStateRemove(och.GuildID, c.me.ID)
		}
	}
	u.voice.demoChannel = ch.ID
	if ch.ID.IsValid() {
		c.st.Cabinet.VoiceStateSet(ch.GuildID, &discord.VoiceState{
			GuildID: ch.GuildID, ChannelID: ch.ID, UserID: c.me.ID,
			SelfMute: u.voice.muted, SelfDeaf: u.voice.deafened,
		}, false)
	}
	c.rebuildRows()
}

// updateVoice handles the voice buttons.
func (u *ui) updateVoice(gtx layout.Context) {
	v := &u.voice
	u.viewerSounds()
	if v.muteBtn.Clicked(gtx) {
		u.toggleVoice(false)
		u.syncDemoVoice()
	}
	if v.deafBtn.Clicked(gtx) {
		u.toggleVoice(true)
		u.syncDemoVoice()
	}
	if v.leaveBtn.Clicked(gtx) {
		u.leaveVoice()
	}
	c := u.chat
	if c == nil {
		return
	}
	if v.channelBtn.Clicked(gtx) {
		if ch, err := c.st.Cabinet.Channel(u.voiceChannel()); err == nil {
			c.openCall(*ch)
		}
	}
	if v.camBtn.Clicked(gtx) {
		u.voiceInfo("The camera is not supported yet")
	}
	if v.screenBtn.Clicked(gtx) {
		c.screenShareClicked()
	}
	if v.activityBtn.Clicked(gtx) {
		u.voiceInfo("Activities only work in Discord's own app")
	}
	if v.soundBtn.Clicked(gtx) {
		c.openSoundboard()
	}
	if v.settingsBtn.Clicked(gtx) {
		c.openSettings()
	}
}

// syncDemoVoice shows the user's mute and deafen in the demo's channel list.
func (u *ui) syncDemoVoice() {
	if c := u.chat; c != nil && u.voice.demoChannel.IsValid() {
		if ch, err := c.st.Cabinet.Channel(u.voice.demoChannel); err == nil {
			c.demoVoice(*ch)
		}
	}
}

// layoutVoicePanel is the box above the user's name while in a voice
// channel, as Discord's: how well connected, the channel, whose name opens
// the call, the button to hang up, and those of the camera, the screen, the
// activities and the soundboard. It also says what the last voice action
// did, or why it failed.
func (u *ui) layoutVoicePanel(gtx layout.Context) layout.Dimensions {
	u.updateVoice(gtx)
	th := u.th
	v := &u.voice
	status := u.voiceStatus()
	msg, failed := u.voiceMessage()
	if !status.ChannelID.IsValid() && msg == "" {
		return layout.Dimensions{}
	}

	size := image.Pt(gtx.Constraints.Max.X, 0)
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var rows []layout.FlexChild
		if status.ChannelID.IsValid() {
			title, col := "Connecting…", colorYellow
			if status.Joined {
				title, col = "Voice Connected", colorGreen
			}
			where := "Voice channel"
			if ch, err := u.chat.st.Cabinet.Channel(status.ChannelID); err == nil {
				where = ch.Name
				if g, err := u.chat.st.Cabinet.Guild(ch.GuildID); err == nil {
					where += " / " + g.Name
				}
			}
			if s := v.session; s != nil && status.Joined {
				if lat := s.Latency(); lat > 0 {
					title += fmt.Sprintf(" · %d ms", lat.Milliseconds())
				}
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconSignal, 16, col) }),
									layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
									layout.Flexed(1, oneLine(bold(label(th, 14, col, title))).Layout),
								)
							}),
							// The channel opens the call in the middle.
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return v.channelBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									pointer.CursorPointer.Add(gtx.Ops)
									c := colorMuted
									if v.channelBtn.Hovered() {
										c = colorText
									}
									return oneLine(label(th, 12, c, where)).Layout(gtx)
								})
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &v.leaveBtn, iconHangUp, colorText, colorRed)
					}),
				)
			}))
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gap := layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout)
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return panelButton(gtx, &v.camBtn, iconCamera) }),
						gap,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							// Lit while the screen is shared, as in Discord.
							if u.streaming() {
								return panelButtonColored(gtx, &v.screenBtn, iconScreen, colorHeader, colorGreenButton)
							}
							return panelButton(gtx, &v.screenBtn, iconScreen)
						}),
						gap,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return panelButton(gtx, &v.activityBtn, iconActivity) }),
						gap,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return panelButton(gtx, &v.soundBtn, iconSoundboard) }),
					)
				})
			}))
		}
		if msg != "" {
			col := colorText
			if failed {
				col = colorRed
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, label(th, 12, col, msg).Layout)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
	call := macro.Stop()
	size.Y = dims.Size.Y
	paint.FillShape(gtx.Ops, colorPanel, clip.Rect{Max: size}.Op())
	// A line separates it from the user's name below.
	paint.FillShape(gtx.Ops, colorSidebar, clip.Rect{Min: image.Pt(0, size.Y-1), Max: size}.Op())
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}

// panelButton is a button of the voice panel: ic on a gray patch as wide as
// it is given.
func panelButton(gtx layout.Context, click *widget.Clickable, ic *widget.Icon) layout.Dimensions {
	return panelButtonColored(gtx, click, ic, colorText, colorInput)
}

// panelButtonColored is panelButton in fg on bg.
func panelButtonColored(gtx layout.Context, click *widget.Clickable, ic *widget.Icon, fg, bg colorT) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(32))
		if click.Hovered() {
			bg = lighten(bg)
		}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 18, fg) })
	})
}

// layoutVoiceButtons are the microphone and sound buttons beside the user's
// name.
func (u *ui) layoutVoiceButtons(gtx layout.Context) layout.Dimensions {
	v := &u.voice
	mic, micCol := iconMic, colorMuted
	if v.muted {
		mic, micCol = iconMicOff, colorRed
	}
	headset, headCol := iconHeadset, colorMuted
	if v.deafened {
		headset, headCol = iconDeafened, colorRed
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, &v.muteBtn, mic, micCol, colorHeader)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, &v.deafBtn, headset, headCol, colorHeader)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, &v.settingsBtn, iconSettings, colorMuted, colorHeader)
		}),
	)
}
