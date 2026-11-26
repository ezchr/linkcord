package gui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
)

// spinnerFrame is how often a spinner moves: 60 frames a second, where Gio's
// own loader redraws the window at every vsync, 144 times a second on a
// 144 Hz screen.
const spinnerFrame = time.Second / 60

// spinnerStyle is a turning arc, as material.Loader draws it, that asks for
// its next redraw only when it moves next.
type spinnerStyle struct {
	Color color.NRGBA
}

// loader is a spinner in the accent color.
func loader(th *material.Theme) spinnerStyle {
	return spinnerStyle{Color: colorAccent}
}

func (l spinnerStyle) Layout(gtx layout.Context) layout.Dimensions {
	diam := max(gtx.Constraints.Min.X, gtx.Constraints.Min.Y)
	if diam == 0 {
		diam = gtx.Dp(24)
	}
	sz := gtx.Constraints.Constrain(image.Pt(diam, diam))
	radius := float32(sz.X / 2)
	defer op.Offset(image.Pt(sz.X/2, sz.X/2)).Push(gtx.Ops).Pop()

	// The angle follows the time rounded to the spinner's frame, so that
	// every spinner on screen moves on the same redraw.
	now := gtx.Now
	if now.IsZero() {
		now = time.Now()
	}
	step := now.Truncate(spinnerFrame)
	dt := float32((time.Duration(step.UnixNano()) % time.Second).Seconds())
	start := dt * math.Pi * 2
	const thickness = .25
	vy, vx := math.Sincos(float64(start))
	inner := radius * (1 - thickness*.5)
	pen := f32.Pt(float32(vx), float32(vy)).Mul(inner)
	center := f32.Pt(0, 0).Sub(pen)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.Move(pen)
	p.Arc(center, center, math.Pi*1.5)
	paint.FillShape(gtx.Ops, l.Color, clip.Stroke{Path: p.End(), Width: radius * thickness}.Op())
	gtx.Execute(op.InvalidateCmd{At: step.Add(spinnerFrame)})
	return layout.Dimensions{Size: sz}
}
