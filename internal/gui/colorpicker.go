package gui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// colorPicker picks a color as people do: the hue on a rainbow, and how
// strong and how bright it is on a square of that hue, as Discord's and
// most programs' pickers.
type colorPicker struct {
	// hue, sat and val are the color, from 0 to 1, kept apart from it so
	// that the hue stays while the color is gray or black.
	hue, sat, val float32

	square, rainbow gesture.Drag
	squareSize      image.Point
	rainbowW        int
	quick           [len(quickColors)]widget.Clickable
}

// quickColors are colors to pick at once, under the picker.
var quickColors = [...]uint32{
	0x000000, 0x1e1f22, 0x2b2d31, 0x5865f2, 0x3ba55c, 0xed4245,
	0xfaa61a, 0xeb459e, 0x00a8fc, 0x9b59b6, 0x1abc9c, 0xffffff,
}

// set has the picker show c.
func (p *colorPicker) set(c colorT) {
	h, s, v := rgbToHSV(c)
	// A gray has no hue of its own: the one shown stays.
	if s > 0 && v > 0 {
		p.hue = h
	}
	p.sat, p.val = s, v
}

// color is the color picked.
func (p *colorPicker) color() colorT {
	return hsvToRGB(p.hue, p.sat, p.val)
}

// update reads the pointer and the quick colors, and reports whether the
// color changed, and whether the change is done, the pointer let go.
func (p *colorPicker) update(gtx layout.Context) (changed, done bool) {
	for {
		e, ok := p.square.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch e.Kind {
		case pointer.Press, pointer.Drag:
			if p.squareSize.X > 0 && p.squareSize.Y > 0 {
				p.sat = clamp01(e.Position.X / float32(p.squareSize.X))
				p.val = 1 - clamp01(e.Position.Y/float32(p.squareSize.Y))
				changed = true
			}
		case pointer.Release, pointer.Cancel:
			changed, done = true, true
		}
	}
	for {
		e, ok := p.rainbow.Update(gtx.Metric, gtx.Source, gesture.Horizontal)
		if !ok {
			break
		}
		switch e.Kind {
		case pointer.Press, pointer.Drag:
			if p.rainbowW > 0 {
				p.hue = clamp01(e.Position.X / float32(p.rainbowW))
				// Picking a hue for a gray gives it some color.
				if p.sat < 0.05 {
					p.sat = 0.7
				}
				if p.val < 0.05 {
					p.val = 0.8
				}
				changed = true
			}
		case pointer.Release, pointer.Cancel:
			changed, done = true, true
		}
	}
	for i := range p.quick {
		if p.quick[i].Clicked(gtx) {
			p.set(rgb(quickColors[i]))
			changed, done = true, true
		}
	}
	return changed, done
}

// layout draws the square, the rainbow and the quick colors, as wide as
// given.
func (p *colorPicker) layout(gtx layout.Context) layout.Dimensions {
	w := gtx.Constraints.Max.X
	sq := image.Pt(w, gtx.Dp(140))
	p.squareSize = sq
	r := gtx.Dp(6)

	// The square: the hue, whiter to the left, darker to the bottom.
	area := clip.UniformRRect(image.Rectangle{Max: sq}, r).Push(gtx.Ops)
	paint.ColorOp{Color: hsvToRGB(p.hue, 1, 1)}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: color.NRGBA{255, 255, 255, 255}, Stop2: f32.Pt(float32(sq.X), 0), Color2: color.NRGBA{255, 255, 255, 0}}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: color.NRGBA{0, 0, 0, 0}, Stop2: f32.Pt(0, float32(sq.Y)), Color2: color.NRGBA{0, 0, 0, 255}}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	pointer.CursorCrosshair.Add(gtx.Ops)
	p.square.Add(gtx.Ops)
	area.Pop()
	// Where the color is on it: a ring of white in one of black.
	at := image.Pt(int(p.sat*float32(sq.X)), int((1-p.val)*float32(sq.Y)))
	ring(gtx, at, gtx.Dp(7), p.color())

	// The rainbow of hues under it.
	y := sq.Y + gtx.Dp(12)
	bar := gtx.Dp(14)
	p.rainbowW = w
	t := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
	area = clip.UniformRRect(image.Rect(0, 0, w, bar), bar/2).Push(gtx.Ops)
	const steps = 6
	for i := range steps {
		x0, x1 := w*i/steps, w*(i+1)/steps
		seg := clip.Rect{Min: image.Pt(x0, 0), Max: image.Pt(x1, bar)}.Push(gtx.Ops)
		paint.LinearGradientOp{
			Stop1: f32.Pt(float32(x0), 0), Color1: hsvToRGB(float32(i)/steps, 1, 1),
			Stop2: f32.Pt(float32(x1), 0), Color2: hsvToRGB(float32(i+1)/steps, 1, 1),
		}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		seg.Pop()
	}
	pointer.CursorPointer.Add(gtx.Ops)
	p.rainbow.Add(gtx.Ops)
	area.Pop()
	ring(gtx, image.Pt(int(p.hue*float32(w)), bar/2), gtx.Dp(8), hsvToRGB(p.hue, 1, 1))
	t.Pop()

	// The quick colors, in a row under the rainbow.
	y += bar + gtx.Dp(12)
	s := gtx.Dp(24)
	gap := max((w-len(quickColors)*s)/max(len(quickColors)-1, 1), gtx.Dp(4))
	for i, q := range quickColors {
		t := op.Offset(image.Pt(i*(s+gap), y)).Push(gtx.Ops)
		p.quick[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return swatch(gtx, rgb(q), unit.Dp(float32(s)/gtx.Metric.PxPerDp))
		})
		t.Pop()
	}
	return layout.Dimensions{Size: image.Pt(w, y+s)}
}

// ring draws a disc of c at at, size across, in a white ring in a dark one,
// which shows on any color.
func ring(gtx layout.Context, at image.Point, size int, c colorT) {
	r := image.Rect(at.X-size, at.Y-size, at.X+size, at.Y+size)
	paint.FillShape(gtx.Ops, color.NRGBA{0, 0, 0, 0x99}, clip.Ellipse(r.Inset(-1)).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Ellipse(r).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, c, clip.Ellipse(r.Inset(gtx.Dp(2))).Op(gtx.Ops))
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }

// hsvToRGB is the color of hue h, saturation s and brightness v, from 0 to 1.
func hsvToRGB(h, s, v float32) colorT {
	h = float32(math.Mod(float64(h), 1)) * 6
	i := int(h)
	f := h - float32(i)
	pv, qv, tv := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	var r, g, b float32
	switch i % 6 {
	case 0:
		r, g, b = v, tv, pv
	case 1:
		r, g, b = qv, v, pv
	case 2:
		r, g, b = pv, v, tv
	case 3:
		r, g, b = pv, qv, v
	case 4:
		r, g, b = tv, pv, v
	default:
		r, g, b = v, pv, qv
	}
	to := func(x float32) uint8 { return uint8(clamp01(x)*255 + 0.5) }
	return colorT{R: to(r), G: to(g), B: to(b), A: 0xff}
}

// rgbToHSV is the hue, saturation and brightness of c, from 0 to 1.
func rgbToHSV(c colorT) (h, s, v float32) {
	r, g, b := float32(c.R)/255, float32(c.G)/255, float32(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	v = hi
	d := hi - lo
	if hi > 0 {
		s = d / hi
	}
	if d == 0 {
		return 0, s, v
	}
	switch hi {
	case r:
		h = (g - b) / d
		if h < 0 {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, v
}
