package gui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// audioTrim trims 48 kHz stereo audio: its sound as bars, tall where there
// is noise and flat where there is silence, and the part kept between two
// handles that are dragged, or moved to where the bars are clicked.
type audioTrim struct {
	pcm []int16
	// start and end are the part kept, as fractions of the whole.
	start, end float32
	// dragging is the handle being dragged: 1 the start, 2 the end.
	dragging int
	// bars are the levels, 0 to 1, of barsN slices of the sound.
	bars  []float32
	barsN int
	// playing is since when the part kept plays, zero when it does not.
	playing time.Time
}

// samplesPerSecond is how many samples, of both channels, a second has.
const samplesPerSecond = 48000 * 2

// minTrim is the shortest part that can be kept.
const minTrim = 300 * time.Millisecond

func newAudioTrim(pcm []int16) *audioTrim {
	return &audioTrim{pcm: pcm, start: 0, end: 1}
}

// duration is how long the whole sound lasts.
func (t *audioTrim) duration() time.Duration {
	return time.Duration(len(t.pcm)) * time.Second / samplesPerSecond
}

// at is the time at fraction f of the sound.
func (t *audioTrim) at(f float32) time.Duration {
	return time.Duration(float64(t.duration()) * float64(f))
}

// kept is the part of the sound between the handles, whole frames of both
// channels.
func (t *audioTrim) kept() []int16 {
	n := len(t.pcm) / 2
	a, b := int(float32(n)*t.start)*2, int(float32(n)*t.end)*2
	a, b = max(0, min(a, len(t.pcm))), max(0, min(b, len(t.pcm)))
	if b <= a {
		return nil
	}
	return t.pcm[a:b]
}

// trimmed reports whether a part was cut off.
func (t *audioTrim) trimmed() bool { return t.start > 0 || t.end < 1 }

// levels are the levels of n slices of the sound, as decibels from -60 to
// 0 made 0 to 1, so that silence stays flat and speech stands out.
func (t *audioTrim) levels(n int) []float32 {
	if n == t.barsN && t.bars != nil {
		return t.bars
	}
	bars := make([]float32, n)
	per := len(t.pcm) / max(n, 1)
	// A long sound is sampled, not read whole, for each bar.
	step := max(1, per/2000) * 2
	for i := range bars {
		from := i * per
		var sum float64
		count := 0
		for j := from; j < min(from+per, len(t.pcm)); j += step {
			v := float64(t.pcm[j]) / 32768
			sum += v * v
			count++
		}
		if count == 0 {
			continue
		}
		rms := math.Sqrt(sum / float64(count))
		db := 20 * math.Log10(max(rms, 1e-6))
		bars[i] = float32(max(0, min(1, (db+60)/60)))
	}
	t.bars, t.barsN = bars, n
	return bars
}

// update follows the handles being dragged over an area w pixels wide.
func (t *audioTrim) update(gtx layout.Context, w int) bool {
	changed := false
	minF := float32(minTrim) / float32(max(t.duration(), minTrim))
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: t, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		f := max(0, min(1, e.Position.X/float32(max(w, 1))))
		switch e.Kind {
		case pointer.Press:
			// The handle nearest where it is pressed moves there.
			if abs32(f-t.start) <= abs32(f-t.end) {
				t.dragging = 1
			} else {
				t.dragging = 2
			}
			fallthrough
		case pointer.Drag:
			switch t.dragging {
			case 1:
				t.start = min(f, t.end-minF)
			case 2:
				t.end = max(f, t.start+minF)
			}
			t.start, t.end = max(0, t.start), min(1, t.end)
			changed = true
		case pointer.Release, pointer.Cancel:
			t.dragging = 0
		}
	}
	return changed
}

// layoutTrim draws the bars, the part kept and its handles, and where it
// plays, height dp high, with the times under it.
func (t *audioTrim) layout(gtx layout.Context, th *material.Theme, height unit.Dp) layout.Dimensions {
	w, h := gtx.Constraints.Max.X, gtx.Dp(height)
	if t.update(gtx, w) {
		t.playing = time.Time{}
	}
	barW, gap := max(gtx.Dp(3), 2), max(gtx.Dp(2), 1)
	n := max(1, w/(barW+gap))
	bars := t.levels(n)
	ks, ke := int(float32(w)*t.start), int(float32(w)*t.end)
	// The part kept is lit behind its bars.
	paint.FillShape(gtx.Ops, withAlpha(colorAccent, 0x22), clip.Rect{Min: image.Pt(ks, 0), Max: image.Pt(ke, h)}.Op())
	for i, lv := range bars {
		x := i * (barW + gap)
		bh := max(gtx.Dp(2), int(lv*float32(h)))
		col := colorSelected
		if x+barW/2 >= ks && x+barW/2 <= ke {
			col = colorText
			if lv < 0.15 {
				// Silence, within the part kept.
				col = colorMuted
			}
		}
		y := (h - bh) / 2
		paint.FillShape(gtx.Ops, col, clip.UniformRRect(image.Rect(x, y, x+barW, y+bh), barW/2).Op(gtx.Ops))
	}
	// The handles, a bar with a knob.
	handle := func(x int) {
		hw := max(gtx.Dp(3), 2)
		paint.FillShape(gtx.Ops, colorAccent, clip.Rect{Min: image.Pt(x-hw/2, 0), Max: image.Pt(x+hw/2+1, h)}.Op())
		k := gtx.Dp(10)
		paint.FillShape(gtx.Ops, colorAccent, clip.Ellipse(image.Rect(x-k/2, h/2-k/2, x+k/2, h/2+k/2)).Op(gtx.Ops))
	}
	handle(ks)
	handle(ke)
	// Where it plays.
	if !t.playing.IsZero() {
		kept := t.at(t.end) - t.at(t.start)
		played := time.Since(t.playing)
		if played < kept {
			x := ks + int(float64(ke-ks)*float64(played)/float64(max(kept, 1)))
			paint.FillShape(gtx.Ops, colorHeader, clip.Rect{Min: image.Pt(x, 0), Max: image.Pt(x+max(gtx.Dp(2), 1), h)}.Op())
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(40 * time.Millisecond)})
		} else {
			t.playing = time.Time{}
		}
	}
	area := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	pointer.CursorColResize.Add(gtx.Ops)
	event.Op(gtx.Ops, t)
	area.Pop()

	// The times of the handles and how long the part kept lasts.
	macro := op.Record(gtx.Ops)
	from, to := t.at(t.start), t.at(t.end)
	txt := fmt.Sprintf("%s – %s · %s", trimTime(from), trimTime(to), trimTime(to-from))
	tgtx := gtx
	tgtx.Constraints.Min = image.Point{}
	ld := label(th, 12, colorMuted, txt).Layout(tgtx)
	call := macro.Stop()
	off := op.Offset(image.Pt(0, h+gtx.Dp(4))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	return layout.Dimensions{Size: image.Pt(w, h+gtx.Dp(4)+ld.Size.Y)}
}

// trimTime is d as m:ss.d.
func trimTime(d time.Duration) string {
	tenths := int(d / (100 * time.Millisecond))
	return fmt.Sprintf("%d:%02d.%d", tenths/600, tenths/10%60, tenths%10)
}
