package golive

import (
	"image"
	"image/color"
	"testing"
)

func TestShrink(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	// Left half black, right half white: halved, one dark and one light.
	for x := 2; x < 4; x++ {
		for y := range 2 {
			src.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, 2, 1))
	shrink(dst, src)
	if got := dst.RGBAAt(0, 0); got.R != 0 || got.A != 0 {
		t.Errorf("left: %v", got)
	}
	if got := dst.RGBAAt(1, 0); got.R != 255 || got.A != 255 {
		t.Errorf("right: %v", got)
	}
	// A checkerboard averages to grey, which sampling would not give.
	for x := range 4 {
		for y := range 2 {
			v := uint8(255 * ((x + y) % 2))
			src.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	shrink(dst, src)
	if got := dst.RGBAAt(0, 0); got.R < 120 || got.R > 135 {
		t.Errorf("checkerboard: %v", got)
	}
}

func BenchmarkShrink1080(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	dst := image.NewRGBA(image.Rect(0, 0, 1000, 562))
	for range b.N {
		shrink(dst, src)
	}
}
