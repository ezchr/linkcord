package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"
)

// testGIF makes a GIF of n frames of w×h: the first fills it red, the others
// paint one pixel each, moving right.
func testGIF(t *testing.T, n, w, h int, delay int) []byte {
	t.Helper()
	pal := color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	for i := range n {
		var f *image.Paletted
		if i == 0 {
			f = image.NewPaletted(image.Rect(0, 0, w, h), pal)
			for p := range f.Pix {
				f.Pix[p] = 1
			}
		} else {
			f = image.NewPaletted(image.Rect(i%w, 0, i%w+1, 1), pal)
			f.Pix[0] = 2
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, delay)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeGIF(t *testing.T) {
	frames, delays, err := decodeGIF(testGIF(t, 4, 8, 8, 5), 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 || len(delays) != 4 {
		t.Fatalf("frames %d, delays %d; want 4 and 4", len(frames), len(delays))
	}
	if delays[1] != 50*time.Millisecond {
		t.Fatalf("delay = %v; want 50ms", delays[1])
	}
	// The later frames keep the red of the first under their blue pixel.
	last := frames[3]
	if r, _, b, _ := last.At(3, 0).RGBA(); b == 0 || r != 0 {
		t.Fatalf("pixel painted by frame 3 = %v", last.At(3, 0))
	}
	if r, _, _, _ := last.At(5, 5).RGBA(); r == 0 {
		t.Fatalf("background from frame 0 lost: %v", last.At(5, 5))
	}

	// A delay of 0 shows for a tenth of a second, as in browsers.
	_, delays, _ = decodeGIF(testGIF(t, 2, 4, 4, 0), 64)
	if delays[0] != 100*time.Millisecond {
		t.Fatalf("zero delay = %v; want 100ms", delays[0])
	}

	// One frame is a still picture.
	if frames, _, _ := decodeGIF(testGIF(t, 1, 4, 4, 5), 64); frames != nil {
		t.Fatalf("one frame decoded as %d frames", len(frames))
	}
}

func TestDecodeGIFBudget(t *testing.T) {
	// 1000×1000 frames take 4 MB each; the budget keeps 12 of 30 and merges
	// the delays of the others, so the animation lasts as long.
	frames, delays, err := decodeGIF(testGIF(t, 30, 1000, 1000, 4), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) > animBudget/(1000*1000*4) {
		t.Fatalf("kept %d frames, over the budget", len(frames))
	}
	var total time.Duration
	for _, d := range delays {
		total += d
	}
	if total != 30*40*time.Millisecond {
		t.Fatalf("total %v; want %v", total, 30*40*time.Millisecond)
	}
}
