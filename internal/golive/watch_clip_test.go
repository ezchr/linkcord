//go:build windows && !no_voice

package golive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tphakala/go-opus/opus"
)

func TestSaveClip(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe is not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.h264")
	gen := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30", "-t", "2",
		"-c:v", "libx264", "-g", "30", "-bsf:v", "h264_mp4toannexb", "-f", "h264", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not make H264: %v %s", err, out)
	}
	h264, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 4000)
	n, err := encoder.Encode(make([]int16, 1920), packet)
	if err != nil {
		t.Fatal(err)
	}

	w := &Watch{}
	w.SetClipLength(time.Minute)
	w.clip.picture(h264, true)
	for range 50 {
		w.clip.sound(packet[:n])
	}
	out := filepath.Join(dir, "clip.mp4")
	if err := w.SaveClip(context.Background(), ffmpeg, 30*time.Second, out); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(probe); !strings.Contains(got, "video") || !strings.Contains(got, "audio") {
		t.Errorf("the clip has %q, want video and audio", got)
	}

	// Nothing kept, nothing to clip.
	empty := &Watch{}
	if err := empty.SaveClip(context.Background(), ffmpeg, time.Second, filepath.Join(dir, "none.mp4")); err == nil {
		t.Error("a clip was made of nothing")
	}
}
