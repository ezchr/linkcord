package gui

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
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
	"golang.org/x/exp/shiny/materialdesign/icons"
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

// iconStopWatching is the button that stops watching a stream.
var iconStopWatching = mustIcon(icons.ActionVisibilityOff)

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
	if err != nil && runtime.GOOS == "windows" {
		// Downloaded once, then the stream opens.
		done := fetchMedia()
		go func() {
			<-done
			u.post(func() {
				if err := mediaFetchErr(); err != nil {
					u.voiceFailed("Could not watch the stream", err)
					return
				}
				c.startWatching(vs)
			})
		}()
		return
	}
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
			// As loud as last chosen for them.
			if vol, ok := c.streamVolumes[vs.UserID]; ok {
				started.SetAudioVolume(vol)
			}
			started.SetAudioMuted(c.streamMuted[vs.UserID])
			started.SetClipLength(time.Duration(u.app.StreamClipMinutes) * time.Minute)
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

// layoutWatchedCard fills a card with the watched stream and reports true,
// or offers to watch it: as on Discord, a stream is only received once
// asked for. The button to watch it is returned to be drawn over the
// avatar's card, at y, under the avatar.
func (c *chatView) layoutWatchedCard(gtx layout.Context, vs discord.VoiceState, size image.Point, radius, y int) (bool, func()) {
	if c.watch.streamer == vs.UserID && c.watch.w != nil {
		c.watch.w.SetView(size.X, size.Y)
	}
	if frame := c.watchedFrame(vs.UserID); frame != nil {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
		igtx := gtx
		igtx.Constraints = layout.Exact(size)
		b := frame.Size()
		scale := min(float32(size.X)/float32(b.X), float32(size.Y)/float32(b.Y)) / gtx.Metric.PxPerDp
		widget.Image{Src: *frame, Fit: widget.Contain, Scale: scale}.Layout(igtx)
		c.layoutStreamInfo(gtx)
		if ids, ok := c.streamViewers(vs); ok && len(ids) > 0 {
			t := op.Offset(image.Pt(gtx.Dp(10), gtx.Dp(10))).Push(gtx.Ops)
			m := op.Record(gtx.Ops)
			d := layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutStreamViewers(gtx, vs.GuildID, ids)
			})
			call := m.Stop()
			paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xa0), clip.UniformRRect(image.Rectangle{Max: d.Size}, gtx.Dp(6)).Op(gtx.Ops))
			call.Add(gtx.Ops)
			t.Pop()
		}
		return true, nil
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
	// The button sits under the avatar, in the middle, clear of the name and
	// "LIVE" at the bottom left; drawn after the avatar, over it if the card
	// is small.
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
	at := image.Pt((size.X-bd.Size.X)/2, max(min(y, size.Y-bd.Size.Y-gtx.Dp(36)), 0))
	return false, func() {
		t := op.Offset(at).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
}

// applyStreamClip keeps the stream watched for clips, as chosen.
func (c *chatView) applyStreamClip() {
	if w := c.watch.w; w != nil {
		w.SetClipLength(time.Duration(c.u.app.StreamClipMinutes) * time.Minute)
	}
}

// streamClipLengths are how long a clip of a stream can be, as offered.
var streamClipLengths = []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

// openStreamClip asks how much of the stream watched to clip.
func (c *chatView) openStreamClip() {
	most := time.Duration(c.u.app.StreamClipMinutes) * time.Minute
	c.openMenu(c.pointerAt, 200, func() []menuItem {
		items := []menuItem{{title: true, text: "Clip the last"}}
		for _, d := range streamClipLengths {
			if d > most {
				break
			}
			d := d
			items = append(items, menuItem{key: d.String(), text: clipLengthName(d), act: func() { c.saveStreamClip(d) }})
		}
		return items
	})
}

// saveStreamClip saves the last d of the stream watched as a video in the
// downloads, and shows it.
func (c *chatView) saveStreamClip(d time.Duration) {
	w := c.watch.w
	if w == nil {
		return
	}
	u := c.u
	name := "stream"
	if guildID := c.selGuild; true {
		name = c.memberName(guildID, w.Streamer(), nil)
	}
	out := freeName(filepath.Join(downloadsDir(), fmt.Sprintf("Linkcord stream clip %s - %s.mp4", time.Now().Format("2006-01-02 15-04-05"), safeFileName(name))))
	u.voiceInfo("Saving the clip of the stream…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ffmpeg, err := needMedia(ctx, "ffmpeg")
		if err == nil {
			err = w.SaveClip(ctx, ffmpeg, d, out)
		}
		u.post(func() {
			if err != nil {
				u.voiceFailed("Could not save the clip", err)
				return
			}
			u.voiceInfo("Clip saved to " + filepath.Base(out))
			showInFolder(out)
		})
	}()
}
