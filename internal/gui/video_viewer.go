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
	closeBtn, pauseBtn, muteBtn, replayBtn widget.Clickable
	focused                                bool
	view                                   imageViewer
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

func (c *chatView) openVideo(source, title string) {
	c.closeVideo()
	ctx, cancel := context.WithCancel(context.Background())
	v := &videoViewer{source: source, title: title, cancel: cancel, status: "Loading video…"}
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
	path, err := downloadVideo(ctx, &http.Client{Timeout: 3 * time.Minute}, v.source)
	if err != nil {
		fail(err)
		return
	}
	defer removeVideoFile(path)
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
			audio.paused.Store(v.paused.Load())
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
		// Catch up to the audio clock without drawing obsolete frames.
		if position-at > 100*time.Millisecond {
			return nil
		}
		op := paint.NewImageOp(frame)
		v.mu.Lock()
		v.frame = &op
		v.status = fmt.Sprintf("%.1f s%s", at.Seconds(), audioWarning)
		v.mu.Unlock()
		u.win.Invalidate()
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

func (c *chatView) layoutVideoViewer(gtx layout.Context) layout.Dimensions {
	v := c.video
	if v.pauseBtn.Clicked(gtx) {
		v.togglePause()
	}
	if v.muteBtn.Clicked(gtx) {
		v.toggleMute()
	}
	if v.replayBtn.Clicked(gtx) {
		c.openVideo(v.source, v.title)
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
								return button(gtx, c.u.th, &v.replayBtn, colorPanel, "Replay")
							}),
						)
					}),
					layout.Rigid(oneLine(label(c.u.th, 12, colorMuted, v.title+" · "+status)).Layout),
				)
			})
		})
	}
	return c.layoutVisualViewer(gtx, &v.view, func(layout.Context) *paint.ImageOp { return frame }, c.closeVideo, footer)
}
