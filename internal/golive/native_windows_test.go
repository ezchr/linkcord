//go:build windows && !no_voice

package golive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCaptureEncode captures the first screen, as a stream does, and
// encodes it with OpenH264, downloaded from Cisco the first time.
func TestCaptureEncode(t *testing.T) {
	sources := Sources(0, 160)
	if len(sources) == 0 || sources[0].Kind != Screen {
		t.Fatalf("no screen in %v", sources)
	}
	if sources[0].Thumb == nil {
		t.Skip("the screen cannot be captured now, as when it is locked or off")
	}
	t.Logf("%d sources, the first %dx%d, thumbnail %v", len(sources), sources[0].W, sources[0].H, sources[0].Thumb.Bounds())

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

	capt := newCapturer(sources[0], true)
	defer capt.close()
	pic, ok, _ := capt.grab()
	if !ok {
		t.Fatal("nothing captured")
	}
	t.Log("captured with", capt.method())
	q := Quality{Height: 720, FPS: 30}
	w, h := q.size(pic.w, pic.h)
	enc, err := newEncoder(w, h, 30, q.bitrate(w, h), 120)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.close()
	buf := make([]byte, w*h*3/2)
	start := time.Now()
	pic.toI420(buf, w, h)
	frame, idr, err := enc.encode(buf, 0)
	if err != nil || frame == nil || !idr {
		t.Fatalf("encode: %d bytes, IDR %v, %v", len(frame), idr, err)
	}
	t.Logf("%dx%d encoded in %v: %d bytes", w, h, time.Since(start), len(frame))

	types := map[byte]bool{}
	for _, nal := range splitNALs(frame) {
		types[nal[0]&0x1F] = true
	}
	for _, typ := range []byte{7, 8, 5} {
		if !types[typ] {
			t.Errorf("no NAL unit of type %d in %v", typ, types)
		}
	}

	// A second picture, after a keyframe, is small.
	pic, _, _ = capt.grab()
	pic.toI420(buf, w, h)
	frame, idr, err = enc.encode(buf, 33)
	if err != nil || idr {
		t.Fatalf("second picture: IDR %v, %v", idr, err)
	}
	t.Logf("second picture: %d bytes", len(frame))
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
