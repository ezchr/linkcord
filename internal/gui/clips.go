package gui

import (
	"encoding/binary"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconClip = mustIcon(icons.ContentContentCut)
	iconStop = mustIcon(icons.AVStop)
)

// clipLengths are how long a clip can be, as offered.
var clipLengths = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 3 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// clipTrack is a clip made: everyone together, or one person.
type clipTrack struct {
	name  string
	user  discord.UserID
	pcm   []int16
	saved string
	// trim is the part of it kept, which plays and is saved.
	trim *audioTrim
}

// keptPCM is the part of the clip kept, all of it if not trimmed.
func (t *clipTrack) keptPCM() []int16 {
	if t.trim == nil {
		return t.pcm
	}
	return t.trim.kept()
}

// clipView is the window that makes clips of the voice channel: who and
// how long, then what was said, together and by person, to hear and save.
type clipView struct {
	open     bool
	everyone widget.Clickable
	picked   map[discord.UserID]bool
	userBtns map[discord.UserID]*widget.Clickable
	length   time.Duration
	lengths  [8]widget.Clickable
	makeBtn  widget.Clickable
	back     widget.Clickable
	close    widget.Clickable
	busy     bool
	err      string
	tracks   []clipTrack
	plays    []widget.Clickable
	saves    []widget.Clickable
	playing  int
	list     widget.List
}

// clipLength is how long the clips kept can be.
func (u *ui) clipLength() time.Duration { return time.Duration(u.app.ClipMinutes) * time.Minute }

// applyLinkcordVoice gives the voice session the Linkcord features chosen:
// saturated voices lowered, and what is said kept for clips.
func (u *ui) applyLinkcordVoice() {
	s := u.voice.session
	if s == nil {
		return
	}
	s.SetAutoLevel(!u.app.NoAutoLevel)
	s.SetClipLength(u.clipLength())
}

func (c *chatView) openClips() {
	v := &c.clips
	v.open, v.tracks, v.err, v.playing = true, nil, "", -1
	if v.length == 0 || v.length > c.u.clipLength() {
		v.length = min(time.Minute, c.u.clipLength())
	}
	v.list.Axis = layout.Vertical
}

// voiceMembers are who is in the voice channel joined, the user too.
func (c *chatView) voiceMembers() []discord.UserID {
	ch := c.u.voiceChannel()
	out := []discord.UserID{c.me.ID}
	if channel, err := c.st.Cabinet.Channel(ch); err == nil && channel.GuildID.IsValid() {
		states, _ := c.st.Cabinet.VoiceStates(channel.GuildID)
		for _, vs := range states {
			if vs.ChannelID == ch && vs.UserID != c.me.ID {
				out = append(out, vs.UserID)
			}
		}
	}
	// The others by name, for them not to move about.
	slices.SortFunc(out[1:], func(a, b discord.UserID) int {
		return strings.Compare(strings.ToLower(c.clipName(a)), strings.ToLower(c.clipName(b)))
	})
	return out
}

func (c *chatView) clipName(id discord.UserID) string {
	guildID := discord.NullGuildID
	if ch, err := c.st.Cabinet.Channel(c.u.voiceChannel()); err == nil {
		guildID = ch.GuildID
	}
	return c.memberName(guildID, id, nil)
}

// makeClip makes the clips chosen, in the background.
func (c *chatView) makeClip() {
	v := &c.clips
	s := c.u.voice.session
	if v.busy {
		return
	}
	if s == nil {
		v.err = "Join a voice channel first: what is said there is what is clipped."
		return
	}
	var users []discord.UserID
	for id, on := range v.picked {
		if on {
			users = append(users, id)
		}
	}
	length := v.length
	v.busy, v.err = true, ""
	go func() {
		clips, err := s.Clip(users, length)
		c.u.post(func() {
			v.busy = false
			if err != nil {
				v.err = "Could not make the clip: " + err.Error()
				return
			}
			if len(clips) == 0 {
				v.err = "Nobody chosen said anything in that time."
				return
			}
			tracks := []clipTrack{{name: "Everyone together", pcm: voice.Mix(clips)}}
			var people []clipTrack
			for id, pcm := range clips {
				people = append(people, clipTrack{name: c.clipName(id), user: id, pcm: pcm})
			}
			slices.SortFunc(people, func(a, b clipTrack) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
			v.tracks = append(tracks, people...)
			for i := range v.tracks {
				v.tracks[i].trim = newAudioTrim(v.tracks[i].pcm)
			}
			v.plays = make([]widget.Clickable, len(v.tracks))
			v.saves = make([]widget.Clickable, len(v.tracks))
			v.playing = -1
		})
	}()
}

// saveClip writes a clip as a WAV file to the downloads, and shows it.
func (c *chatView) saveClip(t *clipTrack) {
	name := fmt.Sprintf("Linkcord clip %s - %s.wav", time.Now().Format("2006-01-02 15-04-05"), safeFileName(t.name))
	p := freeName(filepath.Join(downloadsDir(), name))
	if err := os.WriteFile(p, wavFile(t.keptPCM()), 0o644); err != nil {
		c.clips.err = "Could not save the clip: " + err.Error()
		return
	}
	t.saved = p
	showInFolder(p)
}

// wavFile is pcm, 48 kHz stereo samples, as a WAV file.
func wavFile(pcm []int16) []byte {
	data := len(pcm) * 2
	b := make([]byte, 0, 44+data)
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(36+data))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1) // PCM
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint32(b, 48000)
	b = binary.LittleEndian.AppendUint32(b, 48000*2*2)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(data))
	for _, v := range pcm {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}

func clipLengthName(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return "1 hour"
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}

func (c *chatView) updateClips(gtx layout.Context) {
	v := &c.clips
	u := c.u
	if v.close.Clicked(gtx) {
		c.stopClip()
		v.open = false
		return
	}
	if v.back.Clicked(gtx) {
		c.stopClip()
		v.tracks = nil
	}
	if v.everyone.Clicked(gtx) {
		clear(v.picked)
	}
	for id, click := range v.userBtns {
		if click.Clicked(gtx) {
			if v.picked == nil {
				v.picked = make(map[discord.UserID]bool)
			}
			v.picked[id] = !v.picked[id]
		}
	}
	for i, d := range clipLengths {
		if v.lengths[i].Clicked(gtx) && d <= u.clipLength() {
			v.length = d
		}
	}
	if v.makeBtn.Clicked(gtx) {
		c.makeClip()
	}
	if v.playing >= 0 && (u.voice.effects == nil || !u.voice.effects.Playing()) {
		c.stopClip()
	}
	for i := range v.plays {
		if v.plays[i].Clicked(gtx) {
			if v.playing == i {
				c.stopClip()
				continue
			}
			c.stopClip()
			u.soundLibrary()
			settings := u.voiceSettings()
			u.voice.effects.Play(v.tracks[i].keptPCM(), settings.Output, settings.outputVolume())
			v.playing = i
			if t := v.tracks[i].trim; t != nil {
				t.playing = time.Now()
			}
		}
		if v.saves[i].Clicked(gtx) {
			c.saveClip(&v.tracks[i])
		}
	}
	if v.playing >= 0 {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(250 * time.Millisecond)})
	}
}

func (c *chatView) stopClip() {
	if e := c.u.voice.effects; e != nil && c.clips.playing >= 0 {
		e.Stop()
	}
	c.clips.playing = -1
	for i := range c.clips.tracks {
		if t := c.clips.tracks[i].trim; t != nil {
			t.playing = time.Time{}
		}
	}
}

// layoutClips draws the clip window over everything.
func (c *chatView) layoutClips(gtx layout.Context) {
	c.updateClips(gtx)
	v := &c.clips
	if !v.open {
		return
	}
	th := c.u.th
	size := gtx.Constraints.Max
	v.close.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xb0), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
	w := min(gtx.Dp(480), size.X-gtx.Dp(32))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-gtx.Dp(64))}
	macro := op.Record(gtx.Ops)
	var rows []layout.Widget
	if v.tracks == nil {
		rows = c.clipChoiceRows()
	} else {
		rows = c.clipResultRows()
	}
	dims := rounded(cgtx, colorChat, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return listLayout(gtx, th, &v.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return rows[i](gtx)
			})
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-dims.Size.X)/2, (size.Y-dims.Size.Y)/2)).Push(gtx.Ops)
	solid(gtx, v, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// checkRow is a line that can be ticked, with a picture before its name.
func checkRow(gtx layout.Context, th *material.Theme, click *widget.Clickable, lead layout.Widget, name string, on bool) layout.Dimensions {
	return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(lead),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, oneLine(label(th, 15, colorHeader, name)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d := gtx.Dp(20)
					r := image.Rect(0, 0, d, d)
					if on {
						paint.FillShape(gtx.Ops, colorAccent, clip.UniformRRect(r, gtx.Dp(4)).Op(gtx.Ops))
						gtx.Constraints = layout.Exact(r.Max)
						return layout.Center.Layout(gtx, bold(label(th, 13, colorHeader, "✓")).Layout)
					}
					paint.FillShape(gtx.Ops, colorMuted, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(4)).Path(gtx.Ops), Width: float32(gtx.Dp(2))}.Op())
					return layout.Dimensions{Size: r.Max}
				}),
			)
		})
	}, false)
}

func (c *chatView) clipChoiceRows() []layout.Widget {
	th := c.u.th
	v := &c.clips
	anyPicked := false
	for _, on := range v.picked {
		anyPicked = anyPicked || on
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, bold(label(th, 20, colorHeader, "Clip the Voice Channel")).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, &v.close, iconClose, colorMuted, colorHeader)
				}),
			)
		},
		settingsText(th, fmt.Sprintf("What was said in the last %s is kept. Choose whose and how much.", clipLengthName(c.u.clipLength()))),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		settingsHeading(th, "WHO"),
		func(gtx layout.Context) layout.Dimensions {
			return checkRow(gtx, th, &v.everyone, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMembers, 24, colorText) }, "Everyone", !anyPicked)
		},
	}
	for _, id := range c.voiceMembers() {
		id := id
		click := clickFor(&v.userBtns, id)
		name := c.clipName(id)
		user := discord.User{ID: id}
		if u, err := c.st.Cabinet.User(id); err == nil {
			user = *u
		} else if id == c.me.ID {
			user = c.me
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return checkRow(gtx, th, click, func(gtx layout.Context) layout.Dimensions {
				return c.u.picture(gtx, cdnSize(user.AvatarURLWithType(discord.PNGImage)), 24, colorFor(uint64(id)), name, -1)
			}, name, v.picked[id])
		})
	}
	rows = append(rows,
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		settingsHeading(th, "HOW LONG"),
		func(gtx layout.Context) layout.Dimensions {
			var chips []layout.FlexChild
			for i, d := range clipLengths {
				if d > c.u.clipLength() {
					break
				}
				i, d := i, d
				chips = append(chips, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					bg := colorSecondaryButton
					if v.length == d {
						bg = colorAccent
					}
					return layout.Inset{Right: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &v.lengths[i], bg, clipLengthName(d))
					})
				}))
			}
			return wrapLayout(gtx, chips)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	)
	if v.err != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, v.err).Layout)
		})
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		txt := "Clip the last " + clipLengthName(v.length)
		if v.busy {
			txt = "Making the clip…"
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return button(gtx, th, &v.makeBtn, colorAccent, txt)
	})
	return rows
}

func (c *chatView) clipResultRows() []layout.Widget {
	th := c.u.th
	v := &c.clips
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, &v.back, iconBack, colorMuted, colorHeader)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Flexed(1, bold(label(th, 20, colorHeader, "Clip of the last "+clipLengthName(v.length))).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, &v.close, iconClose, colorMuted, colorHeader)
				}),
			)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	if v.err != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, v.err).Layout)
		})
	}
	for i := range v.tracks {
		i := i
		t := &v.tracks[i]
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			sub := clipLengthName(time.Duration(len(t.pcm)/2) * time.Second / 48000)
			if t.trim != nil && t.trim.trimmed() {
				sub = fmt.Sprintf(tr("%s kept of %s"), trimTime(time.Duration(len(t.keptPCM())/2)*time.Second/48000), sub)
			}
			if t.saved != "" {
				sub += " · saved to " + filepath.Base(t.saved)
			}
			playIc := iconPlay
			if v.playing == i {
				playIc = iconStop
			}
			head := func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return iconBox(gtx, &v.plays[i], playIc, colorHeader) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					layout.Flexed(1, stacked(
						bold(label(th, 15, nameCol(i), t.name)).Layout,
						oneLine(label(th, 12, colorMuted, sub)).Layout,
					)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconBox(gtx, &v.saves[i], iconDownload, colorHeader)
					}),
				)
			}
			if t.trim == nil {
				return head(gtx)
			}
			// Its sound, to keep only a part of it, which plays and is saved.
			return stacked(head, layout.Spacer{Height: unit.Dp(10)}.Layout, func(gtx layout.Context) layout.Dimensions {
				return t.trim.layout(gtx, th, 36)
			})(gtx)
		}))
	}
	return rows
}

// nameCol is the color of a clip's name: everyone together's stands out.
func nameCol(i int) colorT {
	if i == 0 {
		return colorAccent
	}
	return colorHeader
}

// clipLastMinute saves the last minute of the stream watched, or else of
// the voice channel, as the keybind for it does.
func (c *chatView) clipLastMinute() {
	u := c.u
	if c.mainWatch() != nil {
		if u.app.StreamClipMinutes <= 0 {
			u.voiceInfo("Turn on stream clips in Settings to clip streams")
			return
		}
		c.saveStreamClip(time.Minute)
		return
	}
	s := u.voice.session
	if s == nil || !u.voiceStatus().Joined {
		u.voiceInfo("Join a voice channel or watch a stream to clip it")
		return
	}
	if s.ClipLength() <= 0 {
		u.voiceInfo("Turn on voice clips in Settings to clip the voice channel")
		return
	}
	u.voiceInfo("Saving the clip…")
	go func() {
		clips, err := s.Clip(nil, time.Minute)
		u.post(func() {
			if err != nil {
				u.voiceFailed("Could not make the clip", err)
				return
			}
			if len(clips) == 0 {
				u.voiceInfo("Nobody said anything in the last minute")
				return
			}
			t := clipTrack{name: "Everyone together", pcm: voice.Mix(clips)}
			name := fmt.Sprintf("Linkcord clip %s - %s.wav", time.Now().Format("2006-01-02 15-04-05"), safeFileName(t.name))
			p := freeName(filepath.Join(downloadsDir(), name))
			if err := os.WriteFile(p, wavFile(t.pcm), 0o644); err != nil {
				u.voiceFailed("Could not save the clip", err)
				return
			}
			u.voiceInfo("Clip saved to " + filepath.Base(p))
			showInFolder(p)
		})
	}()
}
