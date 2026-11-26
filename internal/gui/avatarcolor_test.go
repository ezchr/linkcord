package gui

import (
	"image"
	"image/color"
	"testing"
)

func fillRect(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

func near(a, b color.NRGBA, d int) bool {
	ab := func(x, y uint8) bool { v := int(x) - int(y); return v <= d && v >= -d }
	return ab(a.R, b.R) && ab(a.G, b.G) && ab(a.B, b.B)
}

func TestPrimaryColor(t *testing.T) {
	orange := color.NRGBA{R: 200, G: 90, B: 30, A: 255}
	blue := color.NRGBA{R: 40, G: 60, B: 200, A: 255}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fillRect(img, img.Rect, orange)
	fillRect(img, image.Rect(0, 0, 64, 12), blue)
	if got := primaryColor(img); !near(got, orange, 8) {
		t.Errorf("mostly orange: got %v", got)
	}

	// Pure white and transparent pixels don't count.
	img = image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fillRect(img, img.Rect, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	fillRect(img, image.Rect(0, 0, 64, 20), color.NRGBA{A: 0})
	fillRect(img, image.Rect(0, 50, 64, 64), blue)
	if got := primaryColor(img); !near(got, blue, 8) {
		t.Errorf("blue on white: got %v", got)
	}

	// A near-white avatar gives a near-white tile, as on Discord.
	pale := color.NRGBA{R: 251, G: 243, B: 243, A: 255}
	img = image.NewNRGBA(image.Rect(0, 0, 32, 32))
	fillRect(img, img.Rect, pale)
	if got := primaryColor(img); !near(got, pale, 8) {
		t.Errorf("pale: got %v", got)
	}

	// Nothing that counts: black.
	img = image.NewNRGBA(image.Rect(0, 0, 16, 16))
	if got := primaryColor(img); got != (color.NRGBA{A: 255}) {
		t.Errorf("transparent: got %v", got)
	}
}
