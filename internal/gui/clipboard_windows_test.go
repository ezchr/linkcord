package gui

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// dib builds a bottom-up BITMAPINFOHEADER bitmap of 2×2 pixels.
func dib(bits int, pixel func(x, y int) []byte) []byte {
	stride := (2*bits/8 + 3) &^ 3
	data := make([]byte, 40+stride*2)
	le := binary.LittleEndian
	le.PutUint32(data[0:], 40)
	le.PutUint32(data[4:], 2)
	le.PutUint32(data[8:], 2) // positive: bottom-up
	le.PutUint16(data[12:], 1)
	le.PutUint16(data[14:], uint16(bits))
	for y := range 2 {
		row := data[40+(1-y)*stride:]
		for x := range 2 {
			copy(row[x*bits/8:], pixel(x, y))
		}
	}
	return data
}

func TestDecodeDIB(t *testing.T) {
	// 32 bits with the alpha byte left at zero, as screenshots come: opaque.
	img, err := decodeDIB(dib(32, func(x, y int) []byte {
		if x == 1 && y == 0 {
			return []byte{0, 0, 255, 0} // BGRA: red
		}
		return []byte{255, 0, 0, 0} // blue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, a := img.At(1, 0).RGBA(); r>>8 != 255 || g != 0 || b != 0 || a>>8 != 255 {
		t.Fatalf("top right = %v; want opaque red (rows are stored bottom-up)", img.At(1, 0))
	}
	if _, _, b, _ := img.At(0, 1).RGBA(); b>>8 != 255 {
		t.Fatalf("bottom left = %v; want blue", img.At(0, 1))
	}

	// 24 bits.
	img, err = decodeDIB(dib(24, func(x, y int) []byte { return []byte{0, 255, 0} }))
	if err != nil {
		t.Fatal(err)
	}
	if _, g, _, a := img.At(0, 0).RGBA(); g>>8 != 255 || a>>8 != 255 {
		t.Fatalf("24-bit pixel = %v; want opaque green", img.At(0, 0))
	}

	if _, err := decodeDIB([]byte{1, 2, 3}); err == nil {
		t.Fatal("garbage decoded without an error")
	}
}

// TestReadClipboardLive reads what is on the real clipboard, set beforehand
// by the caller. It only runs when LINKCORD_CLIPBOARD_TEST is "files" or
// "image".
func TestReadClipboardLive(t *testing.T) {
	want := os.Getenv("LINKCORD_CLIPBOARD_TEST")
	if want == "" {
		t.Skip("LINKCORD_CLIPBOARD_TEST is not set")
	}
	clip, err := readClipboard()
	if err != nil {
		t.Fatal(err)
	}
	switch want {
	case "files":
		if len(clip.files) == 0 {
			t.Fatal("no files read")
		}
		t.Logf("files: %s", strings.Join(clip.files, ", "))
	case "image":
		if len(clip.png) == 0 {
			t.Fatal("no picture read")
		}
		t.Logf("picture: %d bytes of PNG", len(clip.png))
	}
}
