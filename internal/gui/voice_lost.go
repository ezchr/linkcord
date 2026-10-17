package gui

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// While the user is in a voice channel, voice-session.json says which; it
// goes when they leave or Linkcord closes as it should. Found when Linkcord
// starts, Linkcord closed while in it, by force or by a crash: a bar says
// so, with a button to join it again, as Discord's does when the voice is
// lost.

// lostVoiceFile is where the voice channel the user is in is kept.
func lostVoiceFile() string { return filepath.Join(filepath.Dir(logPath()), "voice-session.json") }

type voiceSessionFile struct {
	ChannelID discord.ChannelID `json:"channel_id"`
	GuildID   discord.GuildID   `json:"guild_id"`
}

// lostVoiceView is the bar of a voice channel lost.
type lostVoiceView struct {
	ch                   *discord.Channel
	reconnect, dismissed widget.Clickable
}

// noteVoiceChannel keeps the voice channel the user is in, or that they are
// in none.
func (c *chatView) noteVoiceChannel(vs discord.VoiceState) {
	if c.demo {
		return
	}
	if !vs.ChannelID.IsValid() {
		os.Remove(lostVoiceFile())
		return
	}
	data, err := json.Marshal(voiceSessionFile{ChannelID: vs.ChannelID, GuildID: vs.GuildID})
	if err == nil {
		os.WriteFile(lostVoiceFile(), data, 0o600)
	}
}

// forgetVoiceChannel is that Linkcord closes as it should.
func forgetVoiceChannel() { os.Remove(lostVoiceFile()) }

// checkLostVoice shows the bar if Linkcord closed while in a voice channel.
func (c *chatView) checkLostVoice() {
	if c.demo {
		return
	}
	data, err := os.ReadFile(lostVoiceFile())
	if err != nil {
		return
	}
	os.Remove(lostVoiceFile())
	var f voiceSessionFile
	if json.Unmarshal(data, &f) != nil || !f.ChannelID.IsValid() {
		return
	}
	ch, err := c.st.Cabinet.Channel(f.ChannelID)
	if err != nil {
		return
	}
	c.lostVoice.ch = ch
}

// layoutLostVoice is the bar of the voice channel lost, over the columns.
func (c *chatView) layoutLostVoice(gtx layout.Context) layout.Dimensions {
	v := &c.lostVoice
	if v.ch == nil {
		return layout.Dimensions{}
	}
	if v.dismissed.Clicked(gtx) {
		v.ch = nil
		return layout.Dimensions{}
	}
	if v.reconnect.Clicked(gtx) {
		ch := *v.ch
		v.ch = nil
		c.joinVoice(ch)
		return layout.Dimensions{}
	}
	th := c.u.th
	w := gtx.Constraints.Max.X
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		// The text and its button in the middle, the cross at the right, as
		// Discord's.
		closeW := gtx.Dp(32)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(layout.Spacer{Width: unit.Dp(32)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Max.X = max(gtx.Constraints.Max.X-gtx.Dp(140), gtx.Dp(100))
							txt := fmt.Sprintf(tr("Your voice was disconnected from %s because Linkcord closed unexpectedly."), v.ch.Name)
							l := label(th, 14, colorHeader, txt)
							l.Alignment = text.Middle
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return v.reconnect.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								return outlined(gtx, colorHeader, func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, bold(label(th, 14, colorHeader, "Reconnect")).Layout)
								})
							})
						}),
					)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = closeW
				return iconButton(gtx, &v.dismissed, iconClose, colorHeader, colorText)
			}),
		)
	})
	call := macro.Stop()
	paint.FillShape(gtx.Ops, rgb(0x3a0d0f), clip.Rect{Max: image.Pt(w, dims.Size.Y)}.Op())
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(w, dims.Size.Y)}
}

// outlined draws w with a thin rounded border of col.
func outlined(gtx layout.Context, col colorT, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	r := image.Rectangle{Max: dims.Size}
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(4)).Path(gtx.Ops), Width: float32(max(gtx.Dp(1), 1))}.Op())
	call.Add(gtx.Ops)
	return dims
}
