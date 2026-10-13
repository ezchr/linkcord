package gui

import (
	"bytes"
	"context"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestStreamVideo plays a 1080p clip from a slow server: the first frame
// comes before the download ends, scaled down to the viewer's box, and the
// whole clip decodes. A clip with its index at the end has the index fetched
// on its own, as Discord's servers take ranges.
func TestStreamVideo(t *testing.T) {
	t.Run("index first", func(t *testing.T) { testStreamVideo(t, true) })
	t.Run("index last", func(t *testing.T) { testStreamVideo(t, false) })
}

func testStreamVideo(t *testing.T, faststart bool) {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	ffprobe, err := mediaExecutable("ffprobe")
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	args := []string{"-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30",
		"-f", "lavfi", "-i", "sine", "-t", "6",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac"}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	cmd := exec.CommandContext(ctx, ffmpeg, append(args, path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not make the clip: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sent at about a quarter of the clip per second.
	rate := len(data) / 4
	sent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
			return
		}
		defer close(sent)
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		for off := 0; off < len(data); off += rate / 10 {
			end := min(off+rate/10, len(data))
			if _, err := w.Write(data[off:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer server.Close()

	start := time.Now()
	stream, err := streamVideo(ctx, server.Client(), server.URL+"/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	meta, err := probeVideo(ctx, ffprobe, stream.url)
	if err != nil {
		t.Fatal(err)
	}
	var first time.Duration
	frames := 0
	err = decodeVideo(ctx, ffmpeg, stream.url, meta, 0, func(frame *image.RGBA, at time.Duration) error {
		if frames == 0 {
			first = time.Since(start)
			if got := frame.Bounds().Size(); got != image.Pt(1280, 720) {
				t.Errorf("frame is %v; want 1280x720", got)
			}
		}
		frames++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-sent
	total := time.Since(start)
	t.Logf("first frame after %v, download took %v, %d frames", first, total, frames)
	if first > total/2 {
		t.Errorf("first frame after %v; the download took %v", first, total)
	}
	if frames < 175 {
		t.Errorf("decoded %d frames; want 180", frames)
	}
}
