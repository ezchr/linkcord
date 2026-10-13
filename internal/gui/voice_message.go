package gui

import (
	"context"
	"image"
	"net/http"
	"sync"
	"time"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// isVoiceMessage reports whether an attachment is a recorded voice message.
func isVoiceMessage(a discord.Attachment) bool {
	return a.DurationSecs > 0 && len(a.Waveform) > 0
}

// voicePlayer plays one voice message; one plays at a time.
type voicePlayer struct {
	url    string
	cancel context.CancelFunc

	mu      sync.Mutex
	audio   *videoAudio
	start   time.Duration // where this playing began
	pos     time.Duration
	playing bool
	loading bool
	done    bool // finished, failed or stopped: a click plays it again
	err     string
}

// voiceControls are the widgets of one voice message.
type voiceControls struct {
	play  widget.Clickable
	seek  gesture.Click
	width int
}

func (c *chatView) voiceControls(url string) *voiceControls {
	if c.voiceCtl == nil {
		c.voiceCtl = make(map[string]*voiceControls)
	}
	v, ok := c.voiceCtl[url]
	if !ok {
		v = new(voiceControls)
		c.voiceCtl[url] = v
	}
	return v
}

// playVoice plays url from at, stopping any other voice message or video.
func (c *chatView) playVoice(url string, at time.Duration) {
	c.stopVoice()
	c.closeVideo()
	ctx, cancel := context.WithCancel(context.Background())
	p := &voicePlayer{url: url, cancel: cancel, start: at, pos: at, playing: true, loading: true}
	c.voice = p
	u := c.u
	go func() {
		defer cancel()
		err := p.run(ctx, u, c.volume())
		p.mu.Lock()
		p.playing, p.loading, p.done, p.audio = false, false, true, nil
		if err != nil && ctx.Err() == nil {
			p.err = err.Error()
		} else if ctx.Err() == nil {
			p.pos = 0 // finished: back to the start, as on Discord
		}
		p.mu.Unlock()
		u.win.Invalidate()
	}()
}

func (c *chatView) stopVoice() {
	if c.voice != nil {
		c.voice.cancel()
		c.voice = nil
	}
}

func (p *voicePlayer) run(ctx context.Context, u *ui, volume float32) error {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		return err
	}
	stream, err := streamVideo(ctx, http.DefaultClient, p.url)
	if err != nil {
		return err
	}
	defer stream.Close()
	audio, err := newVideoAudio()
	if err != nil {
		return err
	}
	defer audio.stop()
	audio.cut.Store(1000 - uint32(volume*1000))
	p.mu.Lock()
	p.audio = audio
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- decodeVideoAudio(ctx, ffmpeg, stream.url, p.start, audio) }()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			// Let the queued sound finish.
			for !audio.eof.Load() {
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
					p.update(audio, u)
				}
			}
			return nil
		case <-ctx.Done():
			<-done
			return nil
		case <-tick.C:
			p.update(audio, u)
		}
	}
}

func (p *voicePlayer) update(audio *videoAudio, u *ui) {
	p.mu.Lock()
	played := audio.position()
	if played > 0 {
		p.loading = false
	}
	p.pos = p.start + played
	p.mu.Unlock()
	u.win.Invalidate()
}

func (p *voicePlayer) isDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *voicePlayer) position() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos
}

func (p *voicePlayer) togglePause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playing = !p.playing
	if p.audio != nil {
		p.audio.paused.Store(!p.playing)
	}
}

// layoutVoiceMessage draws a voice message as Discord does: a round play
// button, the waveform, which fills as it plays and moves it when clicked,
// and the time.
func (c *chatView) layoutVoiceMessage(gtx layout.Context, a discord.Attachment) layout.Dimensions {
	th := c.u.th
	url := string(a.URL)
	k := c.voiceControls(url)
	dur := time.Duration(a.DurationSecs * float64(time.Second))

	p := c.voice
	if p != nil && p.url != url {
		p = nil
	}
	if k.play.Clicked(gtx) {
		switch {
		case p == nil:
			c.playVoice(url, 0)
			p = c.voice
		case p.isDone():
			// Finished or failed: play again from where it shows.
			c.playVoice(url, p.position())
			p = c.voice
		default:
			p.togglePause()
		}
	}
	for {
		e, ok := k.seek.Update(gtx.Source)
		if !ok {
			break
		}
		if e.Kind == gesture.KindClick && k.width > 0 && dur > 0 {
			at := time.Duration(float64(dur) * float64(min(max(float32(e.Position.X)/float32(k.width), 0), 1)))
			c.playVoice(url, at)
			p = c.voice
		}
	}

	var pos time.Duration
	playing, loading, errText := false, false, ""
	if p != nil {
		p.mu.Lock()
		pos, playing, loading, errText = p.pos, p.playing, p.loading, p.err
		p.mu.Unlock()
	}
	progress := float32(0)
	if dur > 0 {
		progress = min(float32(float64(pos)/float64(dur)), 1)
	}
	shown := dur
	if p != nil && (playing || pos > 0) {
		shown = pos
	}

	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		return rounded(gtx, colorSidebar, 22, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return k.play.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							return rounded(gtx, colorAccent, 16, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									if loading {
										gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(20), gtx.Dp(20)))
										l := loader(th)
										l.Color = colorHeader
										return l.Layout(gtx)
									}
									ic := iconPlay
									if playing {
										ic = iconPause
									}
									return icon(gtx, ic, 20, colorHeader)
								})
							})
						})
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return c.layoutWaveform(gtx, k, a.Waveform, progress)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if errText != "" {
							return label(th, 12, colorRed, "Could not play").Layout(gtx)
						}
						return label(th, 13, colorText, formatClock(shown)).Layout(gtx)
					}),
				)
			})
		})
	})
}

// layoutWaveform draws Discord's waveform: one bar per sample, played ones
// white.
func (c *chatView) layoutWaveform(gtx layout.Context, k *voiceControls, wave []byte, progress float32) layout.Dimensions {
	w, h := gtx.Constraints.Max.X, gtx.Dp(28)
	k.width = w
	bar, gap := gtx.Dp(3), gtx.Dp(2)
	n := max((w+gap)/(bar+gap), 1)
	for i := range n {
		// Each bar shows the loudest sample of its part of the waveform.
		v := byte(0)
		if len(wave) > 0 {
			from, to := i*len(wave)/n, max((i+1)*len(wave)/n, i*len(wave)/n+1)
			for _, s := range wave[from:min(to, len(wave))] {
				v = max(v, s)
			}
		}
		bh := max(int(float32(h)*float32(v)/255), gtx.Dp(3))
		x := i * (bar + gap)
		col := withAlpha(colorText, 0x70)
		if float32(i)+0.5 <= progress*float32(n) {
			col = colorHeader
		}
		r := image.Rect(x, (h-bh)/2, x+bar, (h+bh)/2)
		paint.FillShape(gtx.Ops, col, clip.UniformRRect(r, bar/2).Op(gtx.Ops))
	}
	defer clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops).Pop()
	pointer.CursorPointer.Add(gtx.Ops)
	k.seek.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(w, h)}
}
