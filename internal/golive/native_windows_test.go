//go:build windows && !no_voice

package golive

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// capture grabs the first screen, or skips the test when it cannot be.
func capture(t testing.TB) (*capturer, picture) {
	sources := Sources(0, 160)
	if len(sources) == 0 || sources[0].Kind != Screen {
		t.Fatalf("no screen in %v", sources)
	}
	if sources[0].Thumb == nil {
		t.Skip("the screen cannot be captured now, as when it is locked or off")
	}
	capt := newCapturer(sources[0], true)
	pic, ok, _ := capt.grab()
	if !ok {
		capt.close()
		t.Fatal("nothing captured")
	}
	return capt, pic
}

// loadOpenH264 loads OpenH264, downloaded from Cisco the first time.
func loadOpenH264(t testing.TB) {
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	path, err := codecPath(ctx, filepath.Join(cache, "linkcord"))
	if err != nil {
		t.Skip("no OpenH264:", err)
	}
	if err := loadCodec(path); err != nil {
		t.Fatal(err)
	}
}

// TestCaptureEncode captures the first screen, as a stream does, and
// encodes it with the graphics card's encoder and with OpenH264.
func TestCaptureEncode(t *testing.T) {
	loadOpenH264(t)
	for _, hw := range []bool{true, false} {
		name := "OpenH264"
		if hw {
			name = "hardware"
		}
		t.Run(name, func(t *testing.T) {
			if !hw {
				t.Setenv(noHardwareEncoder, "1")
			}
			// The graphics card's encoder belongs to the thread it is made on.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			capt, pic := capture(t)
			defer capt.close()
			t.Log("captured with", capt.method())
			q := Quality{Height: 720, FPS: 30}
			w, h := q.size(pic.w, pic.h)
			enc, err := newEncoder(w, h, 30, q.bitrate(w, h), 900)
			if err != nil {
				t.Fatal(err)
			}
			defer enc.close()
			if hw && !enc.hardware() {
				t.Skip("no hardware encoder on this PC")
			}
			t.Log("encoder:", enc.name)
			buf := make([]byte, w*h*3/2)
			var frames [][]byte
			var idrs []bool
			start := time.Now()
			// A hardware encoder may hand each picture back a call later.
			for i := 0; i < 6 && len(frames) < 2; i++ {
				pic, _, _ = capt.grab()
				pic.toYUV(buf, w, h, enc.nv12())
				frame, _, idr, err := enc.encode(buf, int64(i*33))
				if err != nil {
					t.Fatal(err)
				}
				if frame != nil {
					frames = append(frames, frame)
					idrs = append(idrs, idr)
				}
			}
			if len(frames) < 2 {
				t.Fatalf("%d pictures encoded of 6", len(frames))
			}
			t.Logf("%dx%d: 6 pictures in %v, the first %d bytes, the second %d", w, h, time.Since(start), len(frames[0]), len(frames[1]))
			if !idrs[0] || idrs[1] {
				t.Errorf("IDR pictures: %v, want only the first", idrs)
			}
			types := map[byte]bool{}
			for _, nal := range splitNALs(frames[0]) {
				types[nal[0]&0x1F] = true
			}
			for _, typ := range []byte{7, 8, 5} {
				if !types[typ] {
					t.Errorf("no NAL unit of type %d in the first picture: %v", typ, types)
				}
			}
			// A keyframe asked for comes next (or the one after, a call late).
			enc.forceIDR()
			got := false
			for i := 0; i < 3 && !got; i++ {
				pic.toYUV(buf, w, h, enc.nv12())
				_, _, idr, err := enc.encode(buf, int64(300+i*33))
				if err != nil {
					t.Fatal(err)
				}
				got = idr
			}
			if !got {
				t.Error("no keyframe after one was asked for")
			}
		})
	}
}

// TestToYUVBands checks that converting a picture in bands on several
// threads gives what one pass does, scaled or not, and that NV12 holds the
// same colours as I420.
func TestToYUVBands(t *testing.T) {
	capt, pic := capture(t)
	defer capt.close()
	for _, size := range [][2]int{{pic.w &^ 1, pic.h &^ 1}, {1280, 720}, {854, 480}} {
		w, h := size[0], size[1]
		// The plain C conversion is the reference the SSE2 one must match.
		one := make([]byte, w*h*3/2)
		setSIMD(false)
		pic.toYUVOnce(one, w, h, false)
		setSIMD(true)
		banded := make([]byte, w*h*3/2)
		pic.toYUV(banded, w, h, false)
		if string(one) != string(banded) {
			t.Errorf("%dx%d: banded I420 differs from one pass", w, h)
		}
		nv := make([]byte, w*h*3/2)
		pic.toYUV(nv, w, h, true)
		back := make([]byte, w*h*3/2)
		nv12ToI420(back, nv, w, h)
		if string(back) != string(one) {
			t.Errorf("%dx%d: NV12 differs from I420", w, h)
		}
	}
}

func BenchmarkToYUV(b *testing.B) {
	capt, pic := capture(b)
	defer capt.close()
	for _, size := range [][2]int{{pic.w &^ 1, pic.h &^ 1}, {1280, 720}} {
		w, h := size[0], size[1]
		buf := make([]byte, w*h*3/2)
		b.Run(fmt.Sprintf("%dx%d_from_%dx%d_bands", w, h, pic.w, pic.h), func(b *testing.B) {
			for b.Loop() {
				pic.toYUV(buf, w, h, true)
			}
		})
		b.Run(fmt.Sprintf("%dx%d_from_%dx%d_one_thread", w, h, pic.w, pic.h), func(b *testing.B) {
			for b.Loop() {
				pic.toYUVOnce(buf, w, h, true)
			}
		})
	}
}

func BenchmarkEncode(b *testing.B) {
	loadOpenH264(b)
	for _, hw := range []bool{true, false} {
		b.Run(map[bool]string{true: "hardware", false: "OpenH264"}[hw], func(b *testing.B) {
			if !hw {
				b.Setenv(noHardwareEncoder, "1")
			}
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			capt, pic := capture(b)
			defer capt.close()
			w, h := Quality{Height: 720, FPS: 60}.size(pic.w, pic.h)
			enc, err := newEncoder(w, h, 60, Quality{FPS: 60}.bitrate(w, h), 3600)
			if err != nil {
				b.Fatal(err)
			}
			defer enc.close()
			if hw && !enc.hardware() {
				b.Skip("no hardware encoder")
			}
			base := make([]byte, w*h*3/2)
			pic.toYUV(base, w, h, enc.nv12())
			buf := make([]byte, len(base))
			i := int64(0)
			cpu0 := processCPU()
			for b.Loop() {
				// The screen pans 8 pixels a picture, as a game moving: every
				// picture differs, as encoders meet in a real stream.
				shift := int(i*8) % w &^ 1
				for y := 0; y < h; y++ {
					copy(buf[y*w:y*w+w-shift], base[y*w+shift:y*w+w])
					copy(buf[y*w+w-shift:y*w+w], base[y*w:y*w+shift])
				}
				copy(buf[w*h:], base[w*h:])
				if _, _, _, err := enc.encode(buf, i*16); err != nil {
					b.Fatal(err)
				}
				i++
			}
			b.ReportMetric(float64(processCPU()-cpu0)/float64(max(i, 1)), "cpu-ns/op")
		})
	}
}
func TestSplitNALs(t *testing.T) {
	frame := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x68, 3, 0, 0, 0, 0, 1, 0x65, 4}
	nals := splitNALs(frame)
	want := [][]byte{{0x67, 1, 2}, {0x68, 3, 0}, {0x65, 4}}
	if len(nals) != len(want) {
		t.Fatalf("got %v", nals)
	}
	for i := range want {
		if string(nals[i]) != string(want[i]) {
			t.Errorf("NAL %d = % x, want % x", i, nals[i], want[i])
		}
	}
}

// processCPU is the CPU time this process used so far, in nanoseconds.
func processCPU() int64 {
	var c, e, k, u windows.Filetime
	windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u)
	return ((int64(k.HighDateTime)<<32 | int64(k.LowDateTime)) + (int64(u.HighDateTime)<<32 | int64(u.LowDateTime))) * 100
}

// BenchmarkGrab captures the first screen as fast as it goes; the screen
// mostly does not change, so this is the cost of a grab with the pointer
// drawn over the last picture.
func BenchmarkGrab(b *testing.B) {
	capt, _ := capture(b)
	defer capt.close()
	cpu0 := processCPU()
	n := int64(0)
	for b.Loop() {
		capt.grab()
		n++
	}
	b.ReportMetric(float64(processCPU()-cpu0)/float64(max(n, 1)), "cpu-ns/op")
}

// gpuScreen opens the graphics card's capture of the first screen and waits
// for its first picture, or skips the test where there is none.
func gpuScreen(t testing.TB, cursor bool) (*gpuCapture, Source, int, int) {
	sources := Sources(0, 160)
	if len(sources) == 0 || sources[0].Kind != Screen || sources[0].Thumb == nil {
		t.Skip("no screen to capture")
	}
	g := newGPUCapture(sources[0], cursor)
	if g == nil {
		t.Skip("no Direct3D 11")
	}
	for range 40 {
		w, h, status, _, _ := g.grab()
		switch status {
		case gpuPicture:
			return g, sources[0], w, h
		case gpuNever:
			g.close()
			t.Skip("the graphics card cannot capture here")
		}
		time.Sleep(25 * time.Millisecond)
	}
	g.close()
	t.Skip("no picture from the graphics card")
	return nil, Source{}, 0, 0
}

// TestGPUConvertEncode converts the screen on the graphics card, checks the
// colours against the processor's conversion of the same screen, and
// encodes the textures.
func TestGPUConvertEncode(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	g, src, w, h := gpuScreen(t, false)
	defer g.close()
	ow, oh := w&^1, h&^1
	tex, dev, err := g.convert(ow, oh)
	if err != nil {
		t.Fatal(err)
	}
	onGPU := readNV12(tex, ow, oh)
	releaseCOM(tex)
	if onGPU == nil {
		t.Fatal("could not read the texture back")
	}
	// The reference: the very picture the graphics card converted, read
	// back, converted by the formulas of the processor's conversion.
	rgba := g.preview(ow, oh)
	if rgba == nil {
		t.Fatal("could not read the picture back")
	}
	onCPU := make([]byte, ow*oh*3/2)
	px := func(x, y int) (int, int, int) {
		p := rgba.Pix[(y*ow+x)*4:]
		return int(p[0]), int(p[1]), int(p[2])
	}
	for y := 0; y < oh; y++ {
		for x := 0; x < ow; x++ {
			r, gg, b := px(x, y)
			onCPU[y*ow+x] = byte(((66*r + 129*gg + 25*b + 128) >> 8) + 16)
		}
	}
	for y := 0; y < oh/2; y++ {
		for x := 0; x < ow/2; x++ {
			var r, gg, b int
			for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				pr, pg, pb := px(x*2+d[0], y*2+d[1])
				r, gg, b = r+pr, gg+pg, b+pb
			}
			r, gg, b = (r+2)>>2, (gg+2)>>2, (b+2)>>2
			onCPU[ow*oh+y*ow+x*2] = byte(((-38*r - 74*gg + 112*b + 128) >> 8) + 128)
			onCPU[ow*oh+y*ow+x*2+1] = byte(((112*r - 94*gg - 18*b + 128) >> 8) + 128)
		}
	}
	_ = src
	close := func(a, b []byte) float64 {
		n := 0
		for i := range a {
			if d := int(a[i]) - int(b[i]); d >= -3 && d <= 3 {
				n++
			}
		}
		return float64(n) / float64(len(a))
	}
	cy, cuv := close(onGPU[:ow*oh], onCPU[:ow*oh]), close(onGPU[ow*oh:], onCPU[ow*oh:])
	t.Logf("%dx%d: within 3 of the processor's conversion: Y %.1f%%, UV %.1f%%", ow, oh, cy*100, cuv*100)
	if dir := os.Getenv("LC_DIAG"); dir != "" {
		for name, y := range map[string][]byte{"gpu": onGPU, "cpu": onCPU} {
			img := image.NewGray(image.Rect(0, 0, ow, oh))
			copy(img.Pix, y[:ow*oh])
			f, _ := os.Create(filepath.Join(dir, name+".png"))
			png.Encode(f, img)
			f.Close()
		}
	}
	var uvErr int
	for i := ow * oh; i < len(onGPU); i++ {
		d := int(onGPU[i]) - int(onCPU[i])
		uvErr += max(d, -d)
	}
	meanUV := float64(uvErr) / float64(len(onGPU)-ow*oh)
	t.Logf("mean colour difference: %.2f", meanUV)
	// Colour is sampled where H264 puts it, at the left of each pair, where
	// the formulas average the pair: sharp edges differ a little.
	if cy < 0.97 || cuv < 0.9 || meanUV > 1.5 {
		t.Errorf("the graphics card's colours differ from the processor's: Y %.1f%%, UV %.1f%% within 3", cy*100, cuv*100)
	}
	ew, eh := Quality{Height: 720}.size(w, h)
	enc := newEncoderOn(dev, ew, eh, 30, Quality{FPS: 30}.bitrate(ew, eh), 900)
	if enc == nil {
		t.Skip("this graphics card has no encoder taking its textures")
	}
	defer enc.close()
	t.Log("encoder:", enc.name)
	var frames [][]byte
	var idrs []bool
	for i := 0; i < 6 && len(frames) < 2; i++ {
		g.grab()
		tex, dev, err := g.convert(ew, eh)
		if err != nil {
			t.Fatal(err)
		}
		frame, _, idr, err := enc.encodeTexture(tex, int64(i*33))
		releaseCOM(tex)
		releaseCOM(dev)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil {
			frames = append(frames, frame)
			idrs = append(idrs, idr)
		}
	}
	if len(frames) < 2 {
		t.Fatalf("%d pictures encoded of 6", len(frames))
	}
	if !idrs[0] || idrs[1] {
		t.Errorf("IDR pictures: %v, want only the first", idrs)
	}
	types := map[byte]bool{}
	for _, nal := range splitNALs(frames[0]) {
		types[nal[0]&0x1F] = true
	}
	for _, typ := range []byte{7, 8, 5} {
		if !types[typ] {
			t.Errorf("no NAL unit of type %d in the first picture: %v", typ, types)
		}
	}
	t.Logf("%dx%d: first picture %d bytes, second %d", ew, eh, len(frames[0]), len(frames[1]))
}

// BenchmarkGPUPipeline is the whole work of a moving picture on the graphics
// card: grab, convert to 720p NV12, encode.
func BenchmarkGPUPipeline(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	g, _, w, h := gpuScreen(b, true)
	defer g.close()
	ew, eh := Quality{Height: 720}.size(w, h)
	_, dev, err := g.convert(ew, eh)
	if err != nil {
		b.Fatal(err)
	}
	enc := newEncoderOn(dev, ew, eh, 60, Quality{FPS: 60}.bitrate(ew, eh), 3600)
	if enc == nil {
		b.Skip("no encoder taking textures")
	}
	defer enc.close()
	cpu0 := processCPU()
	n := int64(0)
	for b.Loop() {
		g.grab()
		tex, dev, err := g.convert(ew, eh)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, _, err := enc.encodeTexture(tex, n*16); err != nil {
			b.Fatal(err)
		}
		releaseCOM(tex)
		releaseCOM(dev)
		n++
	}
	b.ReportMetric(float64(processCPU()-cpu0)/float64(max(n, 1)), "cpu-ns/op")
}

// BenchmarkCPUPipeline is the same work with the processor converting: what
// Windows 7 and the fallback do (the encoder still the graphics card's).
func BenchmarkCPUPipeline(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	capt, pic := capture(b)
	defer capt.close()
	ew, eh := Quality{Height: 720}.size(pic.w, pic.h)
	enc, err := newEncoder(ew, eh, 60, Quality{FPS: 60}.bitrate(ew, eh), 3600)
	if err != nil {
		b.Fatal(err)
	}
	defer enc.close()
	buf := make([]byte, ew*eh*3/2)
	cpu0 := processCPU()
	n := int64(0)
	for b.Loop() {
		pic, _, _ = capt.grab()
		pic.toYUV(buf, ew, eh, enc.nv12())
		if _, _, _, err := enc.encode(buf, n*16); err != nil {
			b.Fatal(err)
		}
		n++
	}
	b.ReportMetric(float64(processCPU()-cpu0)/float64(max(n, 1)), "cpu-ns/op")
}
