package gui

import (
	"context"
	"fmt"
	"image"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/golive"
)

// streamWatch is the stream of someone else being watched: one at a time,
// as on Discord.
type streamWatch struct {
	w        *golive.Watch
	streamer discord.UserID
	starting bool
	// op is the picture shown, made again only when the stream gives a
	// new one.
	op    paint.ImageOp
	shown *image.RGBA
	// buttons are the "Watch Stream" buttons, by streamer.
	buttons map[discord.UserID]*widget.Clickable
}

// watching reports whether the stream of id is watched or starting.
func (c *chatView) watching(id discord.UserID) bool {
	return c.watch.streamer == id && (c.watch.w != nil || c.watch.starting)
}

// startWatching watches the stream of vs's user, who shares their screen in
// the voice channel the user is in.
func (c *chatView) startWatching(vs discord.VoiceState) {
	u := c.u
	if u.voiceChannel() != vs.ChannelID {
		u.voiceInfo("Join the voice channel to watch the stream")
		return
	}
	c.stopWatching()
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		u.voiceFailed("Could not watch the stream", err)
		return
	}
	// The stream is named for the server of its channel: a voice state does
	// not always carry it, and a guild stream named as a call is not found.
	guildID := vs.GuildID
	if ch, err := c.st.Cabinet.Channel(vs.ChannelID); err == nil {
		guildID = ch.GuildID
	}
	c.watch.streamer, c.watch.starting = vs.UserID, true
	var w *golive.Watch
	params := golive.WatchParams{
		Main:       c.st,
		Subscribe:  func(h any) func() { return subscribeStream(c, h) },
		GuildID:    guildID,
		ChannelID:  vs.ChannelID,
		StreamerID: vs.UserID,
		UserID:     c.me.ID,
		SessionID:  c.inter.sessionID,
		FFmpeg:     ffmpeg,
		OnFrame:    u.win.Invalidate,
		OnEnd: func(err error) {
			u.post(func() {
				if c.watch.w != nil && c.watch.w == w {
					c.watch.w, c.watch.shown = nil, nil
					if err != nil {
						u.voiceInfo("The stream ended: " + err.Error())
					}
				}
			})
		},
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		started, err := golive.StartWatching(ctx, params)
		u.post(func() {
			if !c.watch.starting || c.watch.streamer != vs.UserID {
				// Given up meanwhile.
				if started != nil {
					go started.Stop()
				}
				return
			}
			c.watch.starting = false
			if err != nil {
				u.voiceFailed("Could not watch the stream", err)
				return
			}
			w = started
			c.watch.w = started
		})
	}()
}

// stopWatching stops watching, if a stream is watched.
func (c *chatView) stopWatching() {
	if w := c.watch.w; w != nil {
		go w.Stop()
	}
	c.watch = streamWatch{buttons: c.watch.buttons}
}

// watchedFrame is the picture of the watched stream of id, or nil.
func (c *chatView) watchedFrame(id discord.UserID) *paint.ImageOp {
	if c.watch.streamer != id || c.watch.w == nil {
		return nil
	}
	img := c.watch.w.Frame()
	if img == nil {
		return nil
	}
	if img != c.watch.shown {
		c.watch.op, c.watch.shown = paint.NewImageOp(img), img
	}
	return &c.watch.op
}

// layoutStreamInfo is the size and pictures a second of the watched stream,
// small in the top left corner.
func (c *chatView) layoutStreamInfo(gtx layout.Context) {
	w, h, fps := c.watch.w.Info()
	if w == 0 {
		return
	}
	text := fmt.Sprintf("%d×%d · %d FPS", w, h, fps)
	pad := gtx.Dp(8)
	t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	defer t.Pop()
	igtx := gtx
	igtx.Constraints.Min = image.Point{}
	rounded(igtx, withAlpha(rgb(0x000000), 0xb0), 4, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx,
			label(c.u.th, 11, colorHeader, text).Layout)
	})
}

// layoutWatchedCard fills a card with the watched stream, or offers to
// watch it: as on Discord, a stream is only received once asked for.
func (c *chatView) layoutWatchedCard(gtx layout.Context, vs discord.VoiceState, size image.Point, radius int) bool {
	if frame := c.watchedFrame(vs.UserID); frame != nil {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
		igtx := gtx
		igtx.Constraints = layout.Exact(size)
		b := frame.Size()
		scale := min(float32(size.X)/float32(b.X), float32(size.Y)/float32(b.Y)) / gtx.Metric.PxPerDp
		widget.Image{Src: *frame, Fit: widget.Contain, Scale: scale}.Layout(igtx)
		c.layoutStreamInfo(gtx)
		return true
	}
	if c.watch.buttons == nil {
		c.watch.buttons = make(map[discord.UserID]*widget.Clickable)
	}
	btn := clickOf(c.watch.buttons, vs.UserID)
	if btn.Clicked(gtx) {
		if c.watching(vs.UserID) {
			c.stopWatching()
		} else {
			c.startWatching(vs)
		}
	}
	text := "Watch Stream"
	if c.watching(vs.UserID) {
		text = "Loading stream…"
	}
	// The button sits in the middle of the card, under the picture.
	macro := op.Record(gtx.Ops)
	bgtx := gtx
	bgtx.Constraints.Min = image.Point{}
	bd := btn.Layout(bgtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		bg := withAlpha(rgb(0x000000), 0xb0)
		if btn.Hovered() {
			bg = withAlpha(rgb(0x000000), 0xe0)
		}
		return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx,
				bold(label(c.u.th, 13, colorHeader, text)).Layout)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt((size.X-bd.Size.X)/2, size.Y*3/4-bd.Size.Y/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return false
}
