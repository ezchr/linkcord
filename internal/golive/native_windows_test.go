//go:build windows && !no_voice

package golive

import (
	"context"
	"fmt"
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
