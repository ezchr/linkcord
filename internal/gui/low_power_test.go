package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnimationPoster(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"GIF", testGIF(t, 4, 8, 8, 5)},
		{"APNG with separate poster", compositionAPNG(t, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poster, err := decodeAnimationPoster(tc.data, 64)
			if err != nil {
				t.Fatal(err)
			}
			r, g, b, a := poster.At(0, 0).RGBA()
			if r != 65535 || g != 0 || b != 0 || a != 65535 {
				t.Fatal("preview is not the first red animation frame")
			}
		})
	}
}

func TestLowPowerDownloadAndDiskCache(t *testing.T) {
	data := compositionAPNG(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()
	source := server.URL + "/sticker.png"
	key := fmt.Sprintf("64:%s", source)
	im := &images{
		lowPower: true, entries: make(map[string]*imageEntry),
		client: server.Client(), sem: make(chan struct{}, 1),
		decode: make(chan struct{}, 1), dir: t.TempDir(),
	}
	frames, delays, poster, err := im.download(key, source, 64)
	if err != nil || len(frames) != 0 || len(delays) != 0 || poster == nil {
		t.Fatalf("frames=%d, delays=%d, poster=%v, error=%v", len(frames), len(delays), poster != nil, err)
	}
	server.Close()
	deadline := time.Now().Add(5 * time.Second)
	for im.get(source, 64) == nil {
		if time.Now().After(deadline) {
			t.Fatal("preview did not reload from disk after the server closed")
		}
		time.Sleep(time.Millisecond)
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	entry := im.entries[key]
	if entry.state != imageReady || len(entry.frames) != 0 || entry.cycle != 0 {
		t.Fatal("low-power cache retained an animation")
	}
	if entry.bytes != 4*4*4 || im.cacheBytes != entry.bytes || im.pendingLoads != 0 {
		t.Fatalf("incorrect retained memory or worker accounting: %+v", entry)
	}
}
