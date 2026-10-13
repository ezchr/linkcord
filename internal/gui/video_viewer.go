package gui

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
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
	size := gtx.Constraints.Max
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, v)
	area.Pop()
	if !v.focused {
		gtx.Execute(key.FocusCmd{Tag: v})
		v.focused = true
	}
	if v.closeBtn.Clicked(gtx) {
		c.closeVideo()
		return layout.Dimensions{Size: size}
	}
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
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: v, Name: key.NameEscape},
			pointer.Filter{Target: v, Kinds: pointer.Press | pointer.Release | pointer.Scroll,
				ScrollX: pointer.ScrollRange{Min: -1e6, Max: 1e6},
				ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6}},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.closeVideo()
			return layout.Dimensions{Size: size}
		}
	}
	v.mu.Lock()
	frame, status, ended := v.frame, v.status, v.ended
	v.mu.Unlock()
	paint.FillShape(gtx.Ops, colorServers, clip.Rect{Max: size}.Op())
	return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(label(c.u.th, 16, colorText, v.title)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, c.u.th, &v.closeBtn, colorPanel, "Close")
					}),
				)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if frame == nil {
					return layout.Center.Layout(gtx, label(c.u.th, 14, colorMuted, status).Layout)
				}
				return layout.Center.Layout(gtx, widget.Image{Src: *frame, Fit: widget.Contain}.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pauseText := "Pause"
				if v.paused.Load() {
					pauseText = "Play"
				}
				muteText := "Mute"
				if v.muted.Load() {
					muteText = "Unmute"
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if ended {
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
					layout.Flexed(1, label(c.u.th, 12, colorMuted, status).Layout),
				)
			}),
		)
	})
}
