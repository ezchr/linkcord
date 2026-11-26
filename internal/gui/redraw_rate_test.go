package gui

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
)

// framesInSecond lays out w frame after frame, each at the time the last
// asked to be woken, and counts the frames drawn in one second of an
// otherwise idle window. A widget asking for a redraw with no time (every
// vsync) counts as 60 frames a second.
func framesInSecond(t *testing.T, w func(gtx layout.Context)) int {
	t.Helper()
	start := animEpoch.Add(time.Hour)
	now, frames := start, 0
	for now.Before(start.Add(time.Second)) {
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{Ops: &ops, Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(24, 24))}
		w(gtx)
		router.Frame(&ops)
		frames++
		at, redraw := router.WakeupTime()
		if !redraw {
			break
		}
		if !at.After(now) {
			at = now.Add(time.Second / 60)
		}
		now = at
	}
	return frames
}

func TestIdleRedrawRate(t *testing.T) {
	// An animated emoji whose GIF asks for 10 ms frames, the fastest
	// browsers honour, in a focused window.
	n := 20
	ops := make([]paint.ImageOp, n)
	delays := make([]time.Duration, n)
	for i := range ops {
		ops[i] = paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 2, 2)))
		delays[i] = 10 * time.Millisecond
	}
	im := &images{
		u: &ui{focused: true},
		entries: map[string]*imageEntry{
			"2:emoji": {state: imageReady, op: ops[0], frames: ops, delays: delays, cycle: time.Duration(n) * 10 * time.Millisecond},
		},
	}
	emoji := framesInSecond(t, func(gtx layout.Context) { im.frame(gtx, "emoji", 2) })
	spin := framesInSecond(t, func(gtx layout.Context) { loader(nil).Layout(gtx) })
	t.Logf("frames per second: animated emoji %d (was 100), spinner %d (was 60)", emoji, spin)
	if emoji > 61 {
		t.Fatalf("animated emoji drew %d frames in a second", emoji)
	}
	if spin > 61 {
		t.Fatalf("spinner drew %d frames in a second", spin)
	}

	// Unfocused, the emoji pauses: one frame, then none.
	im.u.focused = false
	if got := framesInSecond(t, func(gtx layout.Context) { im.frame(gtx, "emoji", 2) }); got != 1 {
		t.Fatalf("unfocused window drew %d frames", got)
	}
}

func TestOffscreenAnimationFramesDropped(t *testing.T) {
	op0 := paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	e := &imageEntry{state: imageReady, op: op0, frames: []paint.ImageOp{op0, op0}, bytes: 32, shownAt: time.Now().Add(-time.Minute)}
	im := &images{entries: map[string]*imageEntry{"2:x": e}, cacheBytes: 32}
	im.newFrame()
	if len(im.entries) != 0 || im.cacheBytes != 0 {
		t.Fatalf("animation off screen kept its frames: %d entries, %d bytes", len(im.entries), im.cacheBytes)
	}
}
