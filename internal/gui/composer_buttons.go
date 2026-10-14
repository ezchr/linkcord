package gui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"
)

// pickerButton is a button of the box to write, drawn as Discord draws
// them: a gray glyph whose details are cut out in the box's color, lighter
// when hovered or open.
func pickerButton(gtx layout.Context, click *widget.Clickable, open bool, glyph func(gtx layout.Context, fg, bg colorT, px int)) layout.Dimensions {
	return glyphButton(gtx, click, open, glyph, colorInput)
}

// glyphButton is pickerButton on a background bg, which the glyph's details
// are cut out in.
func glyphButton(gtx layout.Context, click *widget.Clickable, open bool, glyph func(gtx layout.Context, fg, bg colorT, px int), bg colorT) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := gtx.Dp(32)
		fg := colorMuted
		if open || click.Hovered() {
			fg = colorHeader
		}
		px := gtx.Dp(20)
		off := (size - px) / 2
		t := op.Offset(image.Pt(off, off)).Push(gtx.Ops)
		glyph(gtx, fg, bg, px)
		t.Pop()
		return layout.Dimensions{Size: image.Pt(size, size)}
	})
}

// face cuts a face out of a glyph px wide: tall eyes and a crescent smile,
// as on Discord's buttons, centered at cx, cy (in parts of px).
func face(gtx layout.Context, bg colorT, px int, cx, cy float32) {
	p := float32(px)
	eyeW, eyeH := p*0.11, p*0.17
	for _, ex := range []float32{cx - p*0.15, cx + p*0.15} {
		x, y := int(ex-eyeW/2), int(cy-p*0.14-eyeH/2)
		paint.FillShape(gtx.Ops, bg, clip.Ellipse(image.Rect(x, y, x+int(eyeW+0.5), y+int(eyeH+0.5))).Op(gtx.Ops))
	}
	// The smile: between a deep and a shallow curve.
	l, r, y := cx-p*0.24, cx+p*0.24, cy+p*0.07
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(l, y))
	path.QuadTo(f32.Pt(cx, y+p*0.34), f32.Pt(r, y))
	path.QuadTo(f32.Pt(cx, y+p*0.14), f32.Pt(l, y))
	path.Close()
	paint.FillShape(gtx.Ops, bg, clip.Outline{Path: path.End()}.Op())
}

// emojiGlyph is Discord's emoji button: a smiling face cut out of a disc.
func emojiGlyph(gtx layout.Context, fg, bg colorT, px int) {
	paint.FillShape(gtx.Ops, fg, clip.Ellipse(image.Rect(0, 0, px, px)).Op(gtx.Ops))
	face(gtx, bg, px, float32(px)/2, float32(px)/2)
}

// stickerGlyph is Discord's sticker button: a rounded square whose bottom
// right corner is peeled up in a curve, with a smiling face.
func stickerGlyph(gtx layout.Context, fg, bg colorT, px int) {
	p := float32(px)
	r := p * 0.24
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(r, 0))
	path.LineTo(f32.Pt(p-r, 0))
	path.QuadTo(f32.Pt(p, 0), f32.Pt(p, r))
	path.LineTo(f32.Pt(p, p*0.56))
	// The peel: a curve in from the right side to the bottom.
	path.QuadTo(f32.Pt(p*0.6, p*0.6), f32.Pt(p*0.56, p))
	path.LineTo(f32.Pt(r, p))
	path.QuadTo(f32.Pt(0, p), f32.Pt(0, p-r))
	path.LineTo(f32.Pt(0, r))
	path.QuadTo(f32.Pt(0, 0), f32.Pt(r, 0))
	path.Close()
	paint.FillShape(gtx.Ops, fg, clip.Outline{Path: path.End()}.Op())
	// The peeled flap, apart from the square by a thin gap.
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(p, p*0.68))
	path.QuadTo(f32.Pt(p*0.72, p*0.72), f32.Pt(p*0.68, p))
	path.LineTo(f32.Pt(p*0.8, p))
	path.QuadTo(f32.Pt(p, p), f32.Pt(p, p*0.8))
	path.Close()
	paint.FillShape(gtx.Ops, fg, clip.Outline{Path: path.End()}.Op())
	face(gtx, bg, px, p*0.45, p*0.45)
}

// gifGlyph is "GIF" cut out of a rounded box.
func (c *chatView) gifGlyph(gtx layout.Context, fg, bg colorT, px int) {
	h := px * 3 / 4
	y := (px - h) / 2
	r := image.Rect(-px/10, y, px+px/10, y+h)
	paint.FillShape(gtx.Ops, fg, clip.UniformRRect(r, px/6).Op(gtx.Ops))
	st := op.Offset(r.Min).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(r.Size())
	l := bold(label(c.u.th, 9, bg, "GIF"))
	l.Alignment = text.Middle
	layout.Center.Layout(gtx, l.Layout)
	st.Pop()
}
