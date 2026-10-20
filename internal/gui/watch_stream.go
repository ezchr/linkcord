package gui

import (
	"context"
	"fmt"
	"gioui.org/gesture"
	"image"
	"log/slog"
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

// streamWatch is the stream of someone else being watched. Several can be
// watched at once, as on Discord.
type streamWatch struct {
	w        *golive.Watch
	streamer discord.UserID
	starting bool
	// op is the picture shown, made again only when the stream gives a
	// new one.
	op    paint.ImageOp
	shown *image.RGBA
	// hover is the pointer over the stream, which shows its size and
	// pictures a second.
	hover gesture.Hover
}

// watchSet is the streams watched, by streamer.
type watchSet struct {
	list map[discord.UserID]*streamWatch
	// buttons are the "Watch Stream" buttons, by streamer.
	buttons map[discord.UserID]*widget.Clickable
}

// watched is the stream of id watched or starting, or nil.
func (c *chatView) watched(id discord.UserID) *streamWatch {
	return c.watch.list[id]
}

// anyWatched reports whether a stream is watched or starting.
func (c *chatView) anyWatched() bool { return len(c.watch.list) > 0 }

// mainWatch is the stream that the call's stream buttons (clip, stop) act
// on: the one shown big, else the first by streamer.
func (c *chatView) mainWatch() *streamWatch {
	if s := c.watch.list[c.call.focus]; s != nil && s.w != nil {
		return s
	}
	var best *streamWatch
	for _, s := range c.watch.list {
		if s.w != nil && (best == nil || s.streamer < best.streamer) {
			best = s
		}
	}
	return best
}

// watchesChanged keeps what other goroutines read of the streams watched.
func (c *chatView) watchesChanged() {
	c.u.watchingAny.Store(c.anyWatched())
}

// iconStopWatching is the button that stops watching a stream.
var iconStopWatching = mustIcon(icons.ActionVisibilityOff)

// watching reports whether the stream of id is watched or starting.
func (c *chatView) watching(id discord.UserID) bool {
	s := c.watched(id)
	return s != nil && (s.w != nil || s.starting)
}

// startWatching watches the stream of vs's user, who shares their screen in
// the voice channel the user is in.
func (c *chatView) startWatching(vs discord.VoiceState) {
	u := c.u
	if u.voiceChannel() != vs.ChannelID {
		u.voiceInfo("Join the voice channel to watch the stream")
		return
	}
	// Only their stream starts over; the others keep playing.
	c.stopWatchingOne(vs.UserID)
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
	if c.watch.list == nil {
		c.watch.list = make(map[discord.UserID]*streamWatch)
	}
	s := &streamWatch{streamer: vs.UserID, starting: true}
	c.watch.list[vs.UserID] = s
	c.watchesChanged()
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
				if c.watched(vs.UserID) == s && s.w != nil && s.w == w {
					delete(c.watch.list, vs.UserID)
					c.watchesChanged()
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
			if c.watched(vs.UserID) != s || !s.starting {
				// Given up meanwhile.
				if started != nil {
					go started.Stop()
				}
				return
			}
			s.starting = false
			if err != nil {
				delete(c.watch.list, vs.UserID)
				c.watchesChanged()
				u.voiceFailed("Could not watch the stream", err)
				return
			}
			w = started
			s.w = started
			// As loud as last chosen for them.
			if vol, ok := c.streamVolumes[vs.UserID]; ok {
				started.SetAudioVolume(vol)
			}
			started.SetAudioMuted(c.streamMuted[vs.UserID])
			started.SetClipLength(time.Duration(u.app.StreamClipMinutes) * time.Minute)
			go c.watchStall(started, vs)
		})
	}()
}

// stopWatching stops watching every stream watched.
func (c *chatView) stopWatching() {
	for id := range c.watch.list {
		c.stopWatchingOne(id)
	}
}

// stopWatchingOne stops watching the stream of id, if it is watched.
func (c *chatView) stopWatchingOne(id discord.UserID) {
	s := c.watch.list[id]
	if s == nil {
		return
	}
	if s.w != nil {
		go s.w.Stop()
	}
	delete(c.watch.list, id)
	c.watchesChanged()
}

// watchedFrame is the picture of the watched stream of id, or nil.
func (c *chatView) watchedFrame(id discord.UserID) *paint.ImageOp {
	s := c.watched(id)
	if s == nil || s.w == nil {
		return nil
	}
	img := s.w.Frame()
	if img == nil {
		return nil
	}
	if img != s.shown {
		s.op, s.shown = paint.NewImageOp(img), img
	}
	return &s.op
}

// layoutStreamInfo is the size and pictures a second of the watched stream,
// small in the top left corner.
func (c *chatView) layoutStreamInfo(gtx layout.Context, s *streamWatch) {
	w, h, fps := s.w.Info()
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
	s := c.watched(vs.UserID)
	if s != nil && s.w != nil {
		s.w.SetView(size.X, size.Y)
	}
	if frame := c.watchedFrame(vs.UserID); frame != nil {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
		igtx := gtx
		igtx.Constraints = layout.Exact(size)
		b := frame.Size()
		scale := min(float32(size.X)/float32(b.X), float32(size.Y)/float32(b.Y)) / gtx.Metric.PxPerDp
		widget.Image{Src: *frame, Fit: widget.Contain, Scale: scale}.Layout(igtx)
		if s.hover.Update(gtx.Source) {
			c.layoutStreamInfo(gtx, s)
		}
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		pass := pointer.PassOp{}.Push(gtx.Ops)
		s.hover.Add(gtx.Ops)
		pass.Pop()
		area.Pop()
		if ids, ok := c.streamViewers(vs); ok && len(ids) > 0 {
			t := op.Offset(image.Pt(gtx.Dp(10), gtx.Dp(10))).Push(gtx.Ops)
			m := op.Record(gtx.Ops)
			// As small as the viewers, not the card: its backing would
			// otherwise darken the whole stream.
			vgtx := gtx
			vgtx.Constraints.Min = image.Point{}
			d := layout.UniformInset(unit.Dp(6)).Layout(vgtx, func(gtx layout.Context) layout.Dimensions {
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
			c.stopWatchingOne(vs.UserID)
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
	for _, s := range c.watch.list {
		if s.w != nil {
			s.w.SetClipLength(time.Duration(c.u.app.StreamClipMinutes) * time.Minute)
		}
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
	s := c.mainWatch()
	if s == nil {
		return
	}
	w := s.w
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

// watchStall watches the stream again when its video stopped coming for a
// while, unpaused, as the stream server sometimes stops sending to one
// viewer: joining it again is what brings it back.
func (c *chatView) watchStall(w *golive.Watch, vs discord.VoiceState) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	logged := false
	for range t.C {
		// Pictures coming but not drawn: the window stopped redrawing,
		// as after the screen turned off and on. A paint from Windows
		// draws anyway and lets invalidations through again.
		if undrawn := w.Undrawn(); undrawn >= 2*time.Second {
			state := c.u.win.Repaint()
			if !logged {
				slog.Warn("the stream is not being drawn; repainting", "for", undrawn, "window", state)
				logged = true
			}
		} else {
			logged = false
		}
		stalled := w.Stalled()
		// Video that comes but never decrypts has no keys: joining again
		// asks for them anew.
		if locked := w.Undecryptable(); locked >= 10*time.Second {
			slog.Warn("the stream cannot be decrypted; watching it again", "streamer", vs.UserID, "for", locked)
			c.u.post(func() {
				if s := c.watched(vs.UserID); s != nil && s.w == w {
					c.startWatching(vs)
				}
			})
			return
		}
		if stalled < 6*time.Second {
			if w.Ended() {
				return
			}
			continue
		}
		slog.Warn("the stream stopped coming; watching it again", "streamer", vs.UserID, "for", stalled)
		c.u.post(func() {
			if s := c.watched(vs.UserID); s != nil && s.w == w {
				c.startWatching(vs)
			}
		})
		return
	}
}
