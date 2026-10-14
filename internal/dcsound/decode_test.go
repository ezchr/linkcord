package dcsound

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDecodeSoundOgg decodes an Ogg Opus sound, as the soundboard keeps
// some, to 48 kHz stereo.
func TestDecodeSoundOgg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	p := filepath.Join(t.TempDir(), "s.ogg")
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-ac", "1", "-c:a", "libopus", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not make Opus: %v %s", err, out)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := DecodeSound(data)
	if err != nil {
		t.Fatal(err)
	}
	// A second, stereo, at 48 kHz, give or take Opus's padding.
	if n := len(pcm); n < 90000 || n > 100000 {
		t.Errorf("decoded %d samples, want about 96000", n)
	}
}
