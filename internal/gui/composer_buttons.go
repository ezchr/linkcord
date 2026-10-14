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
		glyph(gtx, fg, colorInput, px)
		t.Pop()
		return layout.Dimensions{Size: image.Pt(size, size)}
	})
}

// emojiGlyph is a smiling face: a filled circle with eyes and smile cut out.
func emojiGlyph(gtx layout.Context, fg, bg colorT, px int) {
	paint.FillShape(gtx.Ops, fg, clip.Ellipse(image.Rect(0, 0, px, px)).Op(gtx.Ops))
	eye := max(px/7, 2)
	ey := px * 30 / 100
	paint.FillShape(gtx.Ops, bg, clip.Ellipse(image.Rect(px*31/100, ey, px*31/100+eye, ey+eye+eye/2)).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, bg, clip.Ellipse(image.Rect(px*69/100-eye, ey, px*69/100, ey+eye+eye/2)).Op(gtx.Ops))
	// The smile: the lower half of an ellipse.
	mouth := image.Rect(px*24/100, px*38/100, px*76/100, px*80/100)
	half := clip.Rect(image.Rect(0, (mouth.Min.Y+mouth.Max.Y)/2, px, px)).Push(gtx.Ops)
	paint.FillShape(gtx.Ops, bg, clip.Ellipse(mouth).Op(gtx.Ops))
	half.Pop()
}

// stickerGlyph is a sticker: a rounded square with its bottom right corner
// peeled.
func stickerGlyph(gtx layout.Context, fg, bg colorT, px int) {
	r := float32(px) * 0.22
	fold := float32(px) * 0.38
	p := float32(px)
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(r, 0))
	path.LineTo(f32.Pt(p-r, 0))
	path.QuadTo(f32.Pt(p, 0), f32.Pt(p, r))
	path.LineTo(f32.Pt(p, p-fold))
	path.LineTo(f32.Pt(p-fold, p))
	path.LineTo(f32.Pt(r, p))
	path.QuadTo(f32.Pt(0, p), f32.Pt(0, p-r))
	path.LineTo(f32.Pt(0, r))
	path.QuadTo(f32.Pt(0, 0), f32.Pt(r, 0))
	path.Close()
	paint.FillShape(gtx.Ops, fg, clip.Outline{Path: path.End()}.Op())
	// The peeled corner, cut out of the square.
	gap := float32(px) * 0.09
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(p, p-fold+gap*1.4))
	path.LineTo(f32.Pt(p-fold+gap*1.4, p))
	path.LineTo(f32.Pt(p-fold+gap*1.4, p-fold+gap*1.4+gap))
	path.QuadTo(f32.Pt(p-fold+gap*1.4, p-fold+gap*1.4), f32.Pt(p-fold+gap*2.4, p-fold+gap*1.4))
	path.Close()
	_ = bg
	paint.FillShape(gtx.Ops, fg, clip.Outline{Path: path.End()}.Op())
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
