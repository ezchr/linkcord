package gui

import (
	"image"
	"image/color"
	"testing"
)

func TestImageDIBRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(2, 1, color.NRGBA{0, 0, 255, 255})
	back, err := decodeDIB(imageDIB(img))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := back.At(0, 0).RGBA(); r>>8 != 255 {
		t.Errorf("top left not red: %v", back.At(0, 0))
	}
	if _, _, b, _ := back.At(2, 1).RGBA(); b>>8 != 255 {
		t.Errorf("bottom right not blue: %v", back.At(2, 1))
	}
}
