package gui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// boostGem is Discord's Server Boost gem: a cut stone, flat on top and
// pointed below, with a lighter facet.
func boostGem(gtx layout.Context, size unit.Dp, col colorT) layout.Dimensions {
	px := gtx.Dp(size)
	p := float32(px)
	pt := func(x, y float32) f32.Point { return f32.Pt(x*p, y*p) }
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(pt(0.27, 0.12))
	path.LineTo(pt(0.73, 0.12))
	path.LineTo(pt(0.96, 0.4))
	path.LineTo(pt(0.5, 0.92))
	path.LineTo(pt(0.04, 0.4))
	path.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: path.End()}.Op())
	// The facet: the top of the stone, lighter.
	path.Begin(gtx.Ops)
	path.MoveTo(pt(0.33, 0.22))
	path.LineTo(pt(0.67, 0.22))
	path.LineTo(pt(0.8, 0.4))
	path.LineTo(pt(0.2, 0.4))
	path.Close()
	paint.FillShape(gtx.Ops, withAlpha(rgb(0xffffff), 0x70), clip.Outline{Path: path.End()}.Op())
	return layout.Dimensions{Size: image.Pt(px, px)}
}
