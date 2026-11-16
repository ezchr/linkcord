package gui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// An icon holds every size, each where its entry says, the largest a PNG.
func TestMakeICO(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 9, A: 255})
		}
	}
	ico, err := makeICO(src)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 || int(le.Uint16(ico[4:])) != len(iconSizes) {
		t.Fatalf("header %v", ico[:6])
	}
	end := 6 + 16*len(iconSizes)
	for i, s := range iconSizes {
		e := ico[6+16*i:]
		if int(e[0]) != s%256 || int(e[1]) != s%256 || le.Uint16(e[6:]) != 32 {
			t.Errorf("entry %d: %v", i, e[:16])
		}
		size, off := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		if off != end {
			t.Errorf("entry %d at %d, want %d", i, off, end)
		}
		data := ico[off : off+size]
		end += size
		if s >= 256 {
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() != s {
				t.Errorf("size %d: not a PNG of that size: %v", s, err)
			}
			continue
		}
		if le.Uint32(data) != 40 || int(int32(le.Uint32(data[4:]))) != s || int(int32(le.Uint32(data[8:]))) != 2*s {
			t.Errorf("size %d: bitmap header %v", s, data[:12])
		}
		if want := 40 + 4*s*s + ((s+31)/32)*4*s; size != want {
			t.Errorf("size %d: %d bytes, want %d", s, size, want)
		}
	}
	if end != len(ico) {
		t.Errorf("%d bytes, entries end at %d", len(ico), end)
	}
	if _, err := makeICO(image.NewNRGBA(image.Rectangle{})); err == nil {
		t.Error("an empty picture made an icon")
	}
}
