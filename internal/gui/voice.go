package gui

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/ningen/v3"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconMic      = mustIcon(icons.AVMic)
	iconMicOff   = mustIcon(icons.AVMicOff)
	iconHeadset  = mustIcon(icons.HardwareHeadset)
	iconDeafened = mustIcon(icons.AVVolumeOff)
	iconHangUp   = mustIcon(icons.CommunicationCallEnd)
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

	// muted and deafened are what the user chose with the buttons, which also
	// apply to the next call joined.
	muted, deafened bool

	// demoChannel is the voice channel joined in the demo, which has no
	// session.
	demoChannel discord.ChannelID

	// err says why the last voice action failed, until errUntil.
	err      string
	errUntil time.Time

	muteBtn, deafBtn, leaveBtn widget.Clickable
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
	if s := u.voice.session; s != nil {
		return s.Speaking(id)
	}
	return false
}

// voiceFailed shows why a voice action failed for a while.
func (u *ui) voiceFailed(what string, err error) {
	slog.Warn("voice action failed", "what", what, "err", err)
	u.voice.err = what + ": " + err.Error()
	u.voice.errUntil = time.Now().Add(8 * time.Second)
	time.AfterFunc(8*time.Second+100*time.Millisecond, u.win.Invalidate)
}

// voiceSession returns the session for st, made on first use.
func (u *ui) voiceSession(st *ningen.State) *voice.Session {
	v := &u.voice
	if v.session != nil && v.st == st {
		return v.session
	}
	v.session, v.st = voice.New(st.State, voiceSensitivity), st
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
	if c.demo {
		return nil
	}
	if !c.st.HasPermissions(ch.ID, discord.PermissionConnect) {
		return errors.New("you do not have permission to connect")
	}
	if ch.VoiceUserLimit > 0 && !c.st.HasPermissions(ch.ID, discord.PermissionMoveMembers) {
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
	if c.demo {
		c.demoVoice(ch)
		return
	}
	s := u.voiceSession(c.st)
	muted, deafened := u.voice.muted, u.voice.deafened
	canSpeak := c.st.HasPermissions(ch.ID, discord.PermissionSpeak|discord.PermissionUseVAD)
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
		}
	}()
}

// leaveVoice leaves the voice channel the user is in, if any.
func (u *ui) leaveVoice() {
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
// channel: where, how well connected, and the button to hang up. It also
// says why a voice action failed.
func (u *ui) layoutVoicePanel(gtx layout.Context) layout.Dimensions {
	u.updateVoice(gtx)
	th := u.th
	v := &u.voice
	status := u.voiceStatus()
	errMsg := ""
	if v.err != "" && time.Now().Before(v.errUntil) {
		errMsg = v.err
	}
	if !status.ChannelID.IsValid() && errMsg == "" {
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
				title, col = "Voice connected", colorGreen
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
							layout.Rigid(oneLine(bold(label(th, 14, col, title))).Layout),
							layout.Rigid(oneLine(label(th, 12, colorMuted, where)).Layout),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &v.leaveBtn, iconHangUp, colorText, colorRed)
					}),
				)
			}))
		}
		if errMsg != "" {
			rows = append(rows, layout.Rigid(label(th, 12, colorRed, errMsg).Layout))
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
	)
}
