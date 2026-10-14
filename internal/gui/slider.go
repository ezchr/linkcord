package gui

import (
	"image"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// slider is a horizontal slider drawn as Discord's: a thin light track that
// fills white up to a round thumb.
type slider struct {
	drag     gesture.Drag
	hover    gesture.Hover
	dragging bool
	value    float32 // while dragging
	width    int
}

// sliderResult is what happened to a slider in a frame.
type sliderResult struct {
	value    float32 // the value under the pointer while it is held
	changed  bool    // the value moved
	released bool    // the pointer let go, at value
}

// update reads the pointer. Positions are relative to the track.
func (s *slider) update(gtx layout.Context) sliderResult {
	var r sliderResult
	for {
		e, ok := s.drag.Update(gtx.Metric, gtx.Source, gesture.Horizontal)
		if !ok {
			break
		}
		switch e.Kind {
		case pointer.Press, pointer.Drag:
			if s.width > 0 {
				s.value = min(max(e.Position.X/float32(s.width), 0), 1)
				s.dragging, r.changed = true, true
			}
		case pointer.Release, pointer.Cancel:
			if s.dragging {
				r.released = true
			}
			s.dragging = false
		}
	}
	r.value = s.value
	return r
}

// layout draws the slider at value, or where it is held, width wide.
func (s *slider) layout(gtx layout.Context, width unit.Dp, value float32) layout.Dimensions {
	if s.dragging {
		value = s.value
	}
	w := gtx.Dp(width)
	if width <= 0 {
		w = gtx.Constraints.Max.X
	}
	h := gtx.Dp(20)
	thumb := gtx.Dp(6)
	track := w - 2*thumb
	s.width = track
	hovered := s.hover.Update(gtx.Source)

	mid := h / 2
	th := max(gtx.Dp(2), 1)
	if hovered || s.dragging {
		th = gtx.Dp(3)
	}
	x := thumb + int(value*float32(track))
	bar := image.Rect(thumb, mid-th/2, thumb+track, mid-th/2+th)
	paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x50), clip.UniformRRect(bar, th/2).Op(gtx.Ops))
	fill := image.Rect(thumb, bar.Min.Y, x, bar.Max.Y)
	paint.FillShape(gtx.Ops, colorHeader, clip.UniformRRect(fill, th/2).Op(gtx.Ops))
	if hovered || s.dragging {
		knob := image.Rect(x-thumb, mid-thumb, x+thumb, mid+thumb)
		paint.FillShape(gtx.Ops, colorHeader, clip.Ellipse(knob).Op(gtx.Ops))
	}

	s.input(gtx, thumb, track, h)
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// input has the slider take the pointer over a track track wide, which
// starts thumb from its left, and h high.
func (s *slider) input(gtx layout.Context, thumb, track, h int) {
	s.width = track
	// The whole slider takes the pointer, with the hand cursor; drag
	// positions count from the start of the track.
	off := op.Offset(image.Pt(thumb, 0)).Push(gtx.Ops)
	area := clip.Rect{Min: image.Pt(-thumb, 0), Max: image.Pt(track+thumb, h)}.Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	s.drag.Add(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	s.hover.Add(gtx.Ops)
	pass.Pop()
	area.Pop()
	off.Pop()
}
