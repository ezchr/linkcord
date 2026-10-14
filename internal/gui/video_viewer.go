package gui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconPause      = mustIcon(icons.AVPause)
	iconReplay     = mustIcon(icons.AVReplay)
	iconFullscreen = mustIcon(icons.NavigationFullscreen)
	iconExitFull   = mustIcon(icons.NavigationFullscreenExit)
)

// videoViewer plays one video, in its tile in the chat or in the fullscreen
// layer, as Discord does.
type videoViewer struct {
	source, title string
	cancel        context.CancelFunc
	paused        atomic.Bool
	muted         atomic.Bool
	seek          chan time.Duration

	mu       sync.Mutex
	frame    *paint.ImageOp
	status   string // why it cannot play, if it cannot
	audio    *videoAudio
	ended    bool
	shown    bool // in the fullscreen layer rather than its tile
	pos, dur time.Duration

	// The controls of the tile and of the layer: each is laid out in one
	// place at a time.
	ctl       [2]videoControls
	tileHover gesture.Hover
	focused   bool
	view      imageViewer
}

type videoControls struct {
	play, mute, full widget.Clickable
	volume, bar      slider
}

const (
	inTile = iota
	inLayer
)

func (v *videoViewer) setStatus(u *ui, status string, ended bool) {
	v.mu.Lock()
	v.status, v.ended = status, ended
	v.mu.Unlock()
	u.win.Invalidate()
}

func (c *chatView) closeVideo() {
	if c.video != nil {
		c.video.cancel()
		c.video = nil
	}
}

// openVideo plays a video in its tile, or in the fullscreen layer when shown.
// The tile shows a spinner until the first frame is ready.
func (c *chatView) openVideo(source, title string, shown bool) {
	c.closeVideo()
	c.stopVoice()
	ctx, cancel := context.WithCancel(context.Background())
	v := &videoViewer{source: source, title: title, cancel: cancel, shown: shown, seek: make(chan time.Duration, 1)}
	c.video = v
	c.viewer = nil
	go v.run(ctx, c.u, c.volume())
}

// volume is the volume chosen last, from silent to full.
func (c *chatView) volume() float32 {
	if c.volumeSet {
		return c.lastVolume
	}
	return 1
}

// run downloads and probes the video once, then plays it from the start and
// from wherever it is moved to, until it is closed.
func (v *videoViewer) run(ctx context.Context, u *ui, volume float32) {
	fail := func(err error) {
		if ctx.Err() == nil {
			v.setStatus(u, err.Error(), true)
		}
	}
	ffmpeg, err := needMedia(ctx, "ffmpeg")
	if err != nil {
		fail(err)
		return
	}
	ffprobe, err := needMedia(ctx, "ffprobe")
	if err != nil {
		fail(err)
		return
	}
	stream, err := streamVideo(ctx, http.DefaultClient, v.source)
	if err != nil {
		fail(err)
		return
	}
	defer stream.Close()
	// A failed download is the better reason when the decoder gives up.
	fail = func(err error) {
		if ctx.Err() == nil {
			if derr := stream.Error(); derr != nil {
				err = derr
			}
			v.setStatus(u, err.Error(), true)
		}
	}
	meta, err := probeVideo(ctx, ffprobe, stream.url)
	if err != nil {
		fail(err)
		return
	}
	v.mu.Lock()
	v.dur = meta.duration
	v.mu.Unlock()
	start := time.Duration(0)
	for {
		next, moved, err := v.play(ctx, u, ffmpeg, stream.url, meta, start, volume)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fail(err)
			return
		}
		if !moved {
			// Finished: wait to be played again or moved.
			v.mu.Lock()
			v.ended = true
			if v.dur > 0 {
				v.pos = v.dur
			}
			v.mu.Unlock()
			u.win.Invalidate()
			select {
			case <-ctx.Done():
				return
			case next = <-v.seek:
			}
		}
		v.mu.Lock()
		v.ended = false
		v.pos = next
		volume = v.volumeLocked(volume)
		v.mu.Unlock()
		start = next
	}
}

// volumeLocked is the volume of the sound being played, or def without one.
func (v *videoViewer) volumeLocked(def float32) float32 {
	if v.audio != nil {
		return float32(1000-v.audio.cut.Load()) / 1000
	}
	return def
}

// play plays from start until the end, an error, or a move to another time,
// which it returns with moved set.
func (v *videoViewer) play(ctx context.Context, u *ui, ffmpeg, path string, meta videoMetadata, start time.Duration, volume float32) (time.Duration, bool, error) {
	playCtx, stop := context.WithCancel(ctx)
	defer stop()
	// A move stops this playing and says where to go on.
	var movedTo atomic.Int64
	var didMove atomic.Bool
	go func() {
		select {
		case <-playCtx.Done():
		case at := <-v.seek:
			movedTo.Store(int64(at))
			didMove.Store(true)
			stop()
		}
	}()
	result := func(err error) (time.Duration, bool, error) {
		if didMove.Load() {
			return time.Duration(movedTo.Load()), true, nil
		}
		return 0, false, err
	}
	var audio *videoAudio
	var audioDone chan error
	if meta.audio {
		var err error
		if audio, err = newVideoAudio(); err == nil {
			// The audio decoder starts sooner than the picture, which would
			// otherwise run behind and have its frames dropped at first.
			audio.held.Store(true)
			audio.paused.Store(v.paused.Load())
			audio.cut.Store(1000 - uint32(volume*1000))
			audio.muted.Store(v.muted.Load())
			v.mu.Lock()
			v.audio = audio
			v.mu.Unlock()
			audioDone = make(chan error, 1)
			go func() { audioDone <- decodeVideoAudio(playCtx, ffmpeg, path, start, audio) }()
			defer func() {
				stop()
				<-audioDone
				audio.stop()
				v.mu.Lock()
				v.audio = nil
				v.mu.Unlock()
			}()
		} else {
			audio = nil
		}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	last := time.Now()
	position := start
	first := true
	var drawn time.Time
	updateClock := func() {
		now := time.Now()
		if !v.paused.Load() {
			if audio != nil && !audio.eof.Load() {
				position = start + audio.position()
			} else {
				position += now.Sub(last)
			}
		}
		last = now
	}
	err := decodeVideo(playCtx, ffmpeg, path, meta, start, func(frame *image.RGBA, at time.Duration) error {
		// The first frame shows at once, even while paused, so that a move
		// shows where it went.
		for !first {
			updateClock()
			if !v.paused.Load() && position >= at {
				break
			}
			select {
			case <-playCtx.Done():
				return playCtx.Err()
			case <-ticker.C:
			}
		}
		if first {
			first = false
			last = time.Now()
		} else if position-at > 100*time.Millisecond && time.Since(drawn) < 100*time.Millisecond {
			// Catch up to the audio clock without drawing obsolete frames,
			// but keep drawing some so that the picture never stops.
			return nil
		}
		drawn = time.Now()
		op := paint.NewImageOp(frame)
		v.mu.Lock()
		v.frame = &op
		v.pos = at
		v.mu.Unlock()
		u.win.Invalidate()
		if audio != nil {
			audio.held.Store(false)
		}
		return nil
	})
	if err != nil || didMove.Load() {
		return result(err)
	}
	if audio != nil {
		for !audio.eof.Load() {
			select {
			case <-playCtx.Done():
				return result(nil)
			case <-ticker.C:
			}
		}
	}
	return result(nil)
}

// moveTo plays from at, also after the end.
func (v *videoViewer) moveTo(at time.Duration) {
	select {
	case <-v.seek:
	default:
	}
	v.seek <- at
	v.mu.Lock()
	v.pos = at
	v.mu.Unlock()
}

func (v *videoViewer) isEnded() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ended
}

func (v *videoViewer) togglePause() {
	if v.isEnded() {
		v.paused.Store(false)
		v.moveTo(0)
		return
	}
	paused := !v.paused.Load()
	v.paused.Store(paused)
	v.mu.Lock()
	if v.audio != nil {
		v.audio.paused.Store(paused)
	}
	v.mu.Unlock()
}

func (v *videoViewer) toggleMute() {
	muted := !v.muted.Load()
	v.muted.Store(muted)
	v.mu.Lock()
	if v.audio != nil {
		v.audio.muted.Store(muted)
	}
	v.mu.Unlock()
}

// visible reports whether the video is in the fullscreen layer.
func (v *videoViewer) visible() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.shown
}

func (v *videoViewer) setShown(shown bool) {
	v.mu.Lock()
	v.shown = shown
	v.mu.Unlock()
}

// setVolume passes a volume slider to the sound, from silent to full.
func (c *chatView) setVolume(v *videoViewer, value float32) {
	c.lastVolume, c.volumeSet = value, true
	v.mu.Lock()
	if v.audio != nil {
		v.audio.cut.Store(1000 - uint32(value*1000))
	}
	v.mu.Unlock()
}

// videoLoading reports whether source is the video loading in its tile.
func (c *chatView) videoLoading(source string) bool {
	if c.video == nil || c.video.source != source {
		return false
	}
	c.video.mu.Lock()
	defer c.video.mu.Unlock()
	return c.video.frame == nil && !c.video.ended && c.video.status == ""
}

// formatClock writes a time as Discord does: 0:07, 1:05:09.
func formatClock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// updateControls handles the clicks on one set of controls.
func (c *chatView) updateControls(gtx layout.Context, v *videoViewer, where int) {
	k := &v.ctl[where]
	if k.play.Clicked(gtx) {
		v.togglePause()
	}
	if k.mute.Clicked(gtx) {
		v.toggleMute()
	}
	if k.full.Clicked(gtx) {
		v.setShown(where == inTile)
	}
	if r := k.volume.update(gtx); r.changed {
		c.setVolume(v, r.value)
		if v.muted.Load() && r.value > 0 {
			v.toggleMute()
		}
	}
	v.mu.Lock()
	dur := v.dur
	v.mu.Unlock()
	// The video moves when the bar is let go, not on every step of a drag.
	if r := k.bar.update(gtx); r.released && dur > 0 {
		v.moveTo(time.Duration(float64(r.value) * float64(dur)))
	}
}

// layoutControls draws the bar of a video as Discord has it: play, the time,
// the progress bar, the volume and fullscreen.
func (c *chatView) layoutControls(gtx layout.Context, v *videoViewer, where int) layout.Dimensions {
	th := c.u.th
	k := &v.ctl[where]
	v.mu.Lock()
	pos, dur, ended := v.pos, v.dur, v.ended
	v.mu.Unlock()
	volume := c.volume()
	if v.muted.Load() {
		volume = 0
	}
	progress := float32(0)
	if dur > 0 {
		progress = min(1, float32(float64(pos)/float64(dur)))
		if k.bar.dragging {
			pos = time.Duration(float64(k.bar.value) * float64(dur))
		}
	}
	playIcon := iconPause
	switch {
	case ended:
		playIcon = iconReplay
	case v.paused.Load():
		playIcon = iconPlay
	}
	volIcon := iconVoice
	if v.muted.Load() || c.volume() == 0 {
		volIcon = iconDeafened
	}
	fullIcon := iconFullscreen
	if where == inLayer {
		fullIcon = iconExitFull
	}
	clock := formatClock(pos)
	if dur > 0 {
		clock += " / " + formatClock(dur)
	}
	btn := func(cl *widget.Clickable, ic *widget.Icon) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				col := withAlpha(colorHeader, 0xd0)
				if cl.Hovered() {
					col = colorHeader
				}
				return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icon(gtx, ic, 22, col)
				})
			})
		}
	}

	// On a narrow video the time and the volume slider leave the bar, as on
	// Discord: the time shows in the corner instead (see videoClock).
	narrow := videoNarrow(gtx, gtx.Constraints.Max.X)
	// A dark band behind the controls, as on Discord's player.
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			paint.FillShape(gtx.Ops, withAlpha(colorServers, 0xc8), clip.Rect(r).Op())
			return layout.Dimensions{Size: r.Max}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(btn(&k.play, playIcon)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if narrow {
							return layout.Dimensions{Size: image.Pt(gtx.Dp(6), 0)}
						}
						return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(8)}.Layout(gtx,
							oneLine(label(th, 12, colorHeader, clock)).Layout)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if dur <= 0 {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 1)}
						}
						return k.bar.layout(gtx, 0, progress)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(btn(&k.mute, volIcon)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if narrow {
							return layout.Dimensions{}
						}
						return k.volume.layout(gtx, 64, volume)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
					layout.Rigid(btn(&k.full, fullIcon)),
				)
			})
		})
}

// videoNarrow reports whether a player w pixels wide is too narrow for the
// time and the volume slider in its bar.
func videoNarrow(gtx layout.Context, w int) bool { return w < gtx.Dp(330) }

// videoClock is the time of the video in the top left corner, faint, for
// players too narrow to show it in their bar.
func (c *chatView) videoClock(gtx layout.Context, v *videoViewer) {
	v.mu.Lock()
	pos, dur := v.pos, v.dur
	v.mu.Unlock()
	clock := formatClock(pos)
	if dur > 0 {
		clock += " / " + formatClock(dur)
	}
	defer op.Offset(image.Pt(gtx.Dp(6), gtx.Dp(6))).Push(gtx.Ops).Pop()
	lgtx := gtx
	lgtx.Constraints.Min = image.Point{}
	rounded(lgtx, withAlpha(rgb(0x000000), 0x70), 4, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(5), Right: unit.Dp(5), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx,
			oneLine(label(c.u.th, 11, withAlpha(colorHeader, 0xb0), clock)).Layout)
	})
}

// layoutVideoPicture draws the current frame, fitted and centred, on black.
func layoutVideoPicture(gtx layout.Context, frame *paint.ImageOp) {
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0xff}, clip.Rect{Max: size}.Op())
	if frame == nil {
		return
	}
	fs := frame.Size()
	if fs.X <= 0 || fs.Y <= 0 {
		return
	}
	k := min(float32(size.X)/float32(fs.X), float32(size.Y)/float32(fs.Y))
	w, h := float32(fs.X)*k, float32(fs.Y)*k
	off := f32.Pt((float32(size.X)-w)/2, (float32(size.Y)-h)/2)
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(k, k)).Offset(off)).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: fs}.Push(gtx.Ops).Pop()
	frame.Filter = paint.FilterLinear
	frame.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func (c *chatView) layoutVideoViewer(gtx layout.Context) layout.Dimensions {
	v := c.video
	c.updateControls(gtx, v, inLayer)
	if c.video != v || !v.visible() {
		return layout.Dimensions{}
	}
	v.mu.Lock()
	frame, status := v.frame, v.status
	v.mu.Unlock()
	if v.view.zoom == 0 {
		v.view.zoom = 1
	}
	v.view.openURL = v.source
	if frame != nil {
		size := frame.Size()
		v.view.w, v.view.h = size.X, size.Y
	} else {
		v.view.w, v.view.h = 640, 360
	}
	footer := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rounded(gtx, withAlpha(colorServers, 0xc8), 0, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.layoutControls(gtx, v, inLayer)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					text := v.title
					if frame != nil {
						text += fmt.Sprintf(" · %d×%d · %s", v.view.w, v.view.h, mediaKind(v.source))
					}
					if status != "" {
						text += " · " + status
					}
					return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Bottom: unit.Dp(6)}.Layout(gtx,
						oneLine(label(c.u.th, 12, colorMuted, text)).Layout)
				}),
			)
		})
	}
	return c.layoutVisualViewer(gtx, &v.view, func(layout.Context) *paint.ImageOp { return frame }, func() { v.setShown(false) }, footer)
}
