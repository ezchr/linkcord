//go:build windows && cgo

package wicimg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(1)
	for y := range h {
		for x := range w {
			seed = seed*1664525 + 1013904223
			n := uint8(seed >> 28)
			o := img.PixOffset(x, y)
			img.Pix[o+0] = uint8(x*255/w) + n
			img.Pix[o+1] = uint8(y*255/h) + n
			img.Pix[o+2] = uint8((x+y)*127/(w+h)) + n
			img.Pix[o+3] = 255
		}
	}
	return img
}

func encJPEG(tb testing.TB, w, h int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, photo(w, h), &jpeg.Options{Quality: 90}); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func encPNG(tb testing.TB, img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// emojiGIF is an animated GIF of n frames of w×h, each a moving band over a
// transparent background, disposed to the background.
func emojiGIF(tb testing.TB, n, w, h int) []byte {
	g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: color.Palette(palette.Plan9)}}
	pal := append(color.Palette{color.Transparent}, palette.Plan9[1:]...)
	for i := range n {
		f := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := range h {
			for x := range w {
				if (x+i*3)%w < w/2 && y > h/4 && y < 3*h/4 {
					f.SetColorIndex(x, y, uint8(1+(x+y+i)%200))
				}
			}
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 4)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func TestFit(t *testing.T) {
	for _, c := range []struct{ w, h, s, ew, eh int }{
		{4000, 3000, 400, 400, 300},
		{1000, 333, 100, 100, 34},
		{20, 10, 40, 20, 10},
		{1, 5000, 50, 1, 50},
	} {
		if w, h := Fit(c.w, c.h, c.s); w != c.ew || h != c.eh {
			t.Errorf("Fit(%d,%d,%d) = %d,%d want %d,%d", c.w, c.h, c.s, w, h, c.ew, c.eh)
		}
	}
}

func TestStillJPEG(t *testing.T) {
	data := encJPEG(t, 1600, 1200)
	img, err := Still(data, 200)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 150 {
		t.Fatalf("size %v", img.Bounds())
	}
	// Compared with Go's decoder, shrunk: the colors are about the same.
	src, _ := jpeg.Decode(bytes.NewReader(data))
	ref := image.NewRGBA(img.Bounds())
	draw.ApproxBiLinear.Scale(ref, ref.Bounds(), src, src.Bounds(), draw.Src, nil)
	var diff int64
	for i := range img.Pix {
		d := int64(img.Pix[i]) - int64(ref.Pix[i])
		diff += d * d
	}
	if mse := diff / int64(len(img.Pix)); mse > 60 {
		t.Fatalf("differs from Go's decode: mse %d", mse)
	}
}

func TestStillPNGPremultiplied(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(src.Pix); i += 4 {
		copy(src.Pix[i:], []byte{255, 0, 0, 128})
	}
	img, err := Still(encPNG(t, src), 16)
	if err != nil {
		t.Fatal(err)
	}
	if p := img.Pix[:4]; p[0] < 126 || p[0] > 129 || p[1] != 0 || p[2] != 0 || p[3] != 128 {
		t.Fatalf("pixel %v, want premultiplied red", p)
	}
	// Smaller than size: kept as it is.
	img, err = Still(encPNG(t, src), 100)
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("%v %v", img.Bounds(), err)
	}
}

func TestGIFMatchesGo(t *testing.T) {
	data := emojiGIF(t, 6, 32, 32)
	frames, delays, err := GIF(data, 32, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 6 || len(delays) != 6 || delays[0].Milliseconds() != 40 {
		t.Fatalf("%d frames, delays %v", len(frames), delays)
	}
	g, _ := gif.DecodeAll(bytes.NewReader(data))
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i, f := range g.Image {
		draw.Draw(canvas, f.Bounds(), f, f.Bounds().Min, draw.Over)
		if !bytes.Equal(canvas.Pix, frames[i].(*image.RGBA).Pix) {
			t.Fatalf("frame %d differs from Go's", i)
		}
		draw.Draw(canvas, f.Bounds(), image.Transparent, image.Point{}, draw.Src)
	}
	// A budget of two frames merges the rest, keeping the time.
	frames, delays, err = GIF(data, 16, 2*16*16*4)
	if err != nil || len(frames) != 2 || delays[0]+delays[1] != 240e6 {
		t.Fatalf("%d frames %v %v", len(frames), delays, err)
	}
}

func TestGarbage(t *testing.T) {
	for _, d := range [][]byte{{0}, []byte("GIF89a"), []byte("\x89PNG\r\n\x1a\nxxxx"), bytes.Repeat([]byte{0xff, 0xd8}, 100)} {
		if _, err := Still(d, 64); err == nil {
			t.Errorf("%q decoded", d)
		}
		GIF(d, 64, 1<<20)
	}
	// A truncated picture fails or decodes, without crashing.
	data := encJPEG(t, 800, 600)
	Still(data[:len(data)/2], 100)
	g := emojiGIF(t, 5, 64, 64)
	GIF(g[:len(g)*2/3], 32, 1<<20)
}

// The Go path in the benchmarks is what internal/gui did before: a whole
// decode, then CatmullRom scaling.

func goStill(data []byte, size int) image.Image {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	w, h := Fit(src.Bounds().Dx(), src.Bounds().Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func goGIF(data []byte, size int) []image.Image {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	b := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	canvas := image.NewRGBA(b)
	var out []image.Image
	for _, f := range g.Image {
		draw.Draw(canvas, f.Bounds(), f, f.Bounds().Min, draw.Over)
		w, h := Fit(b.Dx(), b.Dy(), size)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), canvas, b, draw.Src, nil)
		out = append(out, dst)
		draw.Draw(canvas, f.Bounds(), image.Transparent, image.Point{}, draw.Src)
	}
	return out
}

type benchCase struct {
	name string
	data func(testing.TB) []byte
	size int
	gif  bool
}

var benchCases = []benchCase{
	{"jpeg4000x3000@400", func(tb testing.TB) []byte { return encJPEG(tb, 4000, 3000) }, 400, false},
	{"png1024avatar@40", func(tb testing.TB) []byte { return encPNG(tb, photo(1024, 1024)) }, 40, false},
	{"gif128x30frames@48", func(tb testing.TB) []byte { return emojiGIF(tb, 30, 128, 128) }, 48, true},
	{"webp@400", func(tb testing.TB) []byte {
		p := os.Getenv("LINKCORD_BENCH_WEBP")
		if p == "" {
			tb.Skip("LINKCORD_BENCH_WEBP not set")
		}
		d, err := os.ReadFile(p)
		if err != nil {
			tb.Skip(err)
		}
		return d
	}, 400, false},
}

// goBytes is what the Go heap allocated while f ran: with the C side's
// own buffers freed at once, about the peak a decode adds.
func goBytes(f func()) uint64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func BenchmarkDecode(b *testing.B) {
	for _, c := range benchCases {
		for _, path := range []string{"go", "wic"} {
			b.Run(c.name+"/"+path, func(b *testing.B) {
				data := c.data(b)
				run := func() {
					switch {
					case path == "go" && c.gif:
						goGIF(data, c.size)
					case path == "go":
						goStill(data, c.size)
					case c.gif:
						if _, _, err := GIF(data, c.size, 16<<20); err != nil {
							b.Fatal(err)
						}
					default:
						if _, err := Still(data, c.size); err != nil {
							b.Skip(err)
						}
					}
				}
				run()
				b.ReportMetric(float64(goBytes(run)), "goB/decode")
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					run()
				}
			})
		}
	}
}

// With LINKCORD_BENCH_PEAK=1, the test binary prints its peak working set
// as it exits: run one benchmark per process to see what each path needs,
// native buffers included.
func TestMain(m *testing.M) {
	code := m.Run()
	if os.Getenv("LINKCORD_BENCH_PEAK") == "1" {
		var pmc [10]uintptr // PROCESS_MEMORY_COUNTERS
		pmc[0] = unsafe.Sizeof(pmc)
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
		h, _ := syscall.GetCurrentProcess()
		proc.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc[0])), unsafe.Sizeof(pmc))
		fmt.Printf("peak working set: %d KB\n", pmc[2]>>10)
	}
	os.Exit(code)
}
