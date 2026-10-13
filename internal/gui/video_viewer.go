package gui

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type videoViewer struct {
	source, title                          string
	cancel                                 context.CancelFunc
	paused                                 atomic.Bool
	muted                                  atomic.Bool
	mu                                     sync.Mutex
	frame                                  *paint.ImageOp
	status                                 string
	audio                                  *videoAudio
	ended                                  bool
	shown                                  bool // in the fullscreen layer rather than its tile
	closeBtn, pauseBtn, muteBtn, replayBtn widget.Clickable
	// The controls on the tile, apart from those of the layer: each is laid
	// out in one place at a time.
	tilePause, tileFull widget.Clickable
	volume, tileVolume  widget.Float
	focused             bool
	view                imageViewer
}

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
	ctx, cancel := context.WithCancel(context.Background())
	v := &videoViewer{source: source, title: title, cancel: cancel, status: "Loading video…", shown: shown}
	v.volume.Value, v.tileVolume.Value = 1, 1
	if c.volumeSet {
		v.volume.Value, v.tileVolume.Value = c.lastVolume, c.lastVolume
	}
	c.video = v
	c.viewer = nil
	go v.run(ctx, c.u)
}

func (v *videoViewer) run(ctx context.Context, u *ui) {
	fail := func(err error) {
		if ctx.Err() == nil {
			v.setStatus(u, err.Error(), true)
		}
	}
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		fail(err)
		return
	}
	ffprobe, err := mediaExecutable("ffprobe")
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
	path := stream.url
	// A failed download is the better reason when the decoder gives up.
	fail = func(err error) {
		if ctx.Err() == nil {
			if derr := stream.Error(); derr != nil {
				err = derr
			}
			v.setStatus(u, err.Error(), true)
		}
	}
	meta, err := probeVideo(ctx, ffprobe, path)
	if err != nil {
		fail(err)
		return
	}
	playCtx, stop := context.WithCancel(ctx)
	defer stop()
	var audio *videoAudio
	var audioDone chan error
	audioWarning := ""
	if meta.audio {
		audio, err = newVideoAudio()
		if err != nil {
			audioWarning = " · Audio unavailable"
		} else {
			// The audio decoder starts sooner than the picture, which would
			// otherwise run behind and have its frames dropped at first.
			audio.held.Store(true)
			audio.paused.Store(v.paused.Load())
			audio.volume.Store(uint32(v.tileVolume.Value * 1000))
			audio.muted.Store(v.muted.Load())
			v.mu.Lock()
			v.audio = audio
			v.mu.Unlock()
			audioDone = make(chan error, 1)
			go func() { audioDone <- decodeVideoAudio(playCtx, ffmpeg, path, audio) }()
			defer func() {
				stop()
				<-audioDone
				audio.stop()
				v.mu.Lock()
				v.audio = nil
				v.mu.Unlock()
			}()
		}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	last := time.Now()
	position := time.Duration(0)
	started := false
	var drawn time.Time
	updateClock := func() {
		now := time.Now()
		if !v.paused.Load() {
			if audio != nil && !audio.eof.Load() {
				position = audio.position()
			} else {
				position += now.Sub(last)
			}
		}
		last = now
	}
	err = decodeVideo(playCtx, ffmpeg, path, meta, func(frame *image.RGBA, at time.Duration) error {
		if !started {
			last = time.Now()
			started = true
		}
		for {
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
		// Catch up to the audio clock without drawing obsolete frames, but
		// keep drawing some so that the picture never stops.
		if position-at > 100*time.Millisecond && time.Since(drawn) < 100*time.Millisecond {
			return nil
		}
		drawn = time.Now()
		op := paint.NewImageOp(frame)
		v.mu.Lock()
		v.frame = &op
		v.status = fmt.Sprintf("%.1f s%s", at.Seconds(), audioWarning)
		v.mu.Unlock()
		u.win.Invalidate()
		if audio != nil {
			audio.held.Store(false)
		}
		return nil
	})
	if err != nil {
		fail(err)
		return
	}
	if audio != nil {
		for !audio.eof.Load() {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
	v.setStatus(u, "Finished"+audioWarning, true)
}

func (v *videoViewer) isEnded() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ended
}

func (v *videoViewer) togglePause() {
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
	// The two volume sliders follow each other.
	if shown {
		v.volume.Value = v.tileVolume.Value
	} else {
		v.tileVolume.Value = v.volume.Value
	}
}

// setVolume passes the slider to the sound, from silent to full.
func (c *chatView) setVolume(v *videoViewer, value float32) {
	c.lastVolume, c.volumeSet = value, true
	v.mu.Lock()
	if v.audio != nil {
		v.audio.volume.Store(uint32(value * 1000))
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
	return c.video.frame == nil && !c.video.ended
}

// layoutVideoControls draws the bar under a playing video: play and pause,
// the volume, the time and the button to the other view.
func (c *chatView) layoutVideoControls(gtx layout.Context, v *videoViewer, pause, swap *widget.Clickable, volume *widget.Float, swapText, status string) layout.Dimensions {
	th := c.u.th
	pauseText := "Pause"
	if v.paused.Load() {
		pauseText = "Play"
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return button(gtx, th, pause, colorPanel, pauseText)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(label(th, 12, colorMuted, "Vol").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(90)
			gtx.Constraints.Max.X = gtx.Dp(90)
			s := material.Slider(th, volume)
			s.Color = colorAccent
			return s.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx,
				oneLine(label(th, 12, colorHeader, status)).Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return button(gtx, th, swap, colorPanel, swapText)
		}),
	)
}

func (c *chatView) layoutVideoViewer(gtx layout.Context) layout.Dimensions {
	v := c.video
	if v.volume.Update(gtx) {
		c.setVolume(v, v.volume.Value)
	}
	if v.pauseBtn.Clicked(gtx) {
		v.togglePause()
	}
	if v.muteBtn.Clicked(gtx) {
		v.toggleMute()
	}
	if v.replayBtn.Clicked(gtx) {
		c.openVideo(v.source, v.title, true)
		v = c.video
	}
	v.mu.Lock()
	frame, status, ended := v.frame, v.status, v.ended
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
		return rounded(gtx, withAlpha(colorPopup, 0xe0), 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pauseText := "Pause"
				if v.paused.Load() {
					pauseText = "Play"
				}
				muteText := "Mute"
				if v.muted.Load() {
					muteText = "Unmute"
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if ended || frame == nil {
									gtx = gtx.Disabled()
								}
								return button(gtx, c.u.th, &v.pauseBtn, colorPanel, pauseText)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, c.u.th, &v.muteBtn, colorPanel, muteText)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(110)
								gtx.Constraints.Max.X = gtx.Dp(110)
								s := material.Slider(c.u.th, &v.volume)
								s.Color = colorAccent
								return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, s.Layout)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, c.u.th, &v.replayBtn, colorPanel, "Replay")
							}),
						)
					}),
					layout.Rigid(oneLine(label(c.u.th, 12, colorMuted, v.title+" · "+status)).Layout),
				)
			})
		})
	}
	return c.layoutVisualViewer(gtx, &v.view, func(layout.Context) *paint.ImageOp { return frame }, func() { v.setShown(false) }, footer)
}
