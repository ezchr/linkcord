package gui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// withOrientation returns a JPEG of img that says it is turned as o.
func withOrientation(t *testing.T, img image.Image, o uint16) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	jpg := buf.Bytes()
	tiff := []byte("II*\x00\x08\x00\x00\x00\x01\x00")
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:], 0x0112)
	binary.LittleEndian.PutUint16(entry[2:], 3)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint16(entry[8:], o)
	tiff = append(append(tiff, entry...), 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	return append(append(append([]byte{}, jpg[:2]...), append(app1, seg...)...), jpg[2:]...)
}

// TestExifOrientation turns a photo taken on its side upright, as a phone
// says it is to be shown.
func TestExifOrientation(t *testing.T) {
	// 40 wide, 20 high, red on the left half.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			c := color.RGBA{0, 0, 255, 255}
			if x < 20 {
				c = color.RGBA{255, 0, 0, 255}
			}
			src.Set(x, y, c)
		}
	}
	data := withOrientation(t, src, 6)
	if o := exifOrientation(data); o != 6 {
		t.Fatalf("orientation %d, want 6", o)
	}
	img, err := decodeScaled(data, 100)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("turned to %v, want 20×40", b.Size())
	}
	// Turned clockwise, the left, red, is on top.
	if r, _, _, _ := img.At(10, 5).RGBA(); r < 0x8000 {
		t.Error("the left of the photo is not on top")
	}
}
