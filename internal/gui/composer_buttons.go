package gui

import (
	"bytes"
	_ "embed"
	"image/color"
	"image/png"
	"sync"
	"image"

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

// The emoji and sticker buttons are Discord's own icons, as masks.
var (
	//go:embed icons/emoji.png
	emojiMaskPNG []byte
	//go:embed icons/sticker.png
	stickerMaskPNG []byte

	maskMu    sync.Mutex
	maskCache = map[string]map[colorT]paint.ImageOp{}
)

// maskGlyph draws the mask data px wide in fg.
func maskGlyph(gtx layout.Context, name string, data []byte, fg colorT, px int) {
	maskMu.Lock()
	byColor := maskCache[name]
	if byColor == nil {
		byColor = map[colorT]paint.ImageOp{}
		maskCache[name] = byColor
	}
	img, ok := byColor[fg]
	if !ok {
		src, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			maskMu.Unlock()
			return
		}
		b := src.Bounds()
		out := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				_, _, _, a := src.At(x, y).RGBA()
				out.SetNRGBA(x, y, color.NRGBA{fg.R, fg.G, fg.B, uint8(uint32(fg.A) * (a >> 8) / 255)})
			}
		}
		img = paint.NewImageOp(out)
		byColor[fg] = img
	}
	maskMu.Unlock()
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	widget.Image{Src: img, Fit: widget.Contain, Scale: float32(px) / float32(img.Size().X) / gtx.Metric.PxPerDp}.Layout(gtx)
}

// emojiGlyph is Discord's emoji button.
func emojiGlyph(gtx layout.Context, fg, _ colorT, px int) {
	maskGlyph(gtx, "emoji", emojiMaskPNG, fg, px)
}

// stickerGlyph is Discord's sticker button.
func stickerGlyph(gtx layout.Context, fg, _ colorT, px int) {
	maskGlyph(gtx, "sticker", stickerMaskPNG, fg, px)
}

// gifGlyph is "GIF" cut out of a rounded box.
func (c *chatView) gifGlyph(gtx layout.Context, fg, bg colorT, px int) {
	r := image.Rect(0, 0, px, px)
	paint.FillShape(gtx.Ops, fg, clip.UniformRRect(r, px/6).Op(gtx.Ops))
	st := op.Offset(r.Min).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(r.Size())
	l := bold(label(c.u.th, 8, bg, "GIF"))
	l.Alignment = text.Middle
	layout.Center.Layout(gtx, l.Layout)
	st.Pop()
}
