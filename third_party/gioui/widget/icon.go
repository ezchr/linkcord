// SPDX-License-Identifier: Unlicense OR MIT

package widget

import (
	"image"
	"image/color"
	"image/draw"

	"gioui.org/internal/f32color"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"golang.org/x/exp/shiny/iconvg"
)

type Icon struct {
	src []byte
	// Cached pictures, the latest last. One icon is often drawn at more
	// than one size or color in a frame, as a button and its hover: a
	// single cached picture would be rasterized again each time.
	cache []iconImage
}

type iconImage struct {
	op    paint.ImageOp
	size  int
	color color.NRGBA
}

// maxIconImages bounds the pictures an Icon keeps.
const maxIconImages = 6

const defaultIconSize = unit.Dp(24)

// NewIcon returns a new Icon from IconVG data.
func NewIcon(data []byte) (*Icon, error) {
	_, err := iconvg.DecodeMetadata(data)
	if err != nil {
		return nil, err
	}
	return &Icon{src: data}, nil
}

// Layout displays the icon with its size set to the X minimum constraint.
func (ic *Icon) Layout(gtx layout.Context, color color.NRGBA) layout.Dimensions {
	sz := gtx.Constraints.Min.X
	if sz == 0 {
		sz = gtx.Dp(defaultIconSize)
	}
	size := gtx.Constraints.Constrain(image.Pt(sz, sz))
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()

	ico := ic.image(size.X, color)
	ico.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{
		Size: ico.Size(),
	}
}

func (ic *Icon) image(sz int, color color.NRGBA) paint.ImageOp {
	for i := len(ic.cache) - 1; i >= 0; i-- {
		if c := ic.cache[i]; c.size == sz && c.color == color {
			return c.op
		}
	}
	m, _ := iconvg.DecodeMetadata(ic.src)
	dx, dy := m.ViewBox.AspectRatio()
	img := image.NewRGBA(image.Rectangle{Max: image.Point{X: sz, Y: int(float32(sz) * dy / dx)}})
	var ico iconvg.Rasterizer
	ico.SetDstImage(img, img.Bounds(), draw.Src)
	m.Palette[0] = f32color.NRGBAToLinearRGBA(color)
	iconvg.Decode(&ico, ic.src, &iconvg.DecodeOptions{
		Palette: &m.Palette,
	})
	op := paint.NewImageOp(img)
	if len(ic.cache) == maxIconImages {
		ic.cache = append(ic.cache[:0], ic.cache[1:]...)
	}
	ic.cache = append(ic.cache, iconImage{op: op, size: sz, color: color})
	return op
}
