package golive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestSPSSize reads the size from the SPS of streams FFmpeg encodes, at sizes
// that need cropping.
func TestSPSSize(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	for _, size := range [][2]int{{1920, 1080}, {1280, 720}, {854, 480}, {2560, 1440}} {
		out := filepath.Join(t.TempDir(), "s.h264")
		cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size="+strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1])+":rate=30",
			"-frames:v", "1", "-c:v", "libx264", "-profile:v", "high", out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("could not encode: %v %s", err, b)
		}
		data, _ := os.ReadFile(out)
		var sps []byte
		for _, nal := range splitNALs(data) {
			if nal[0]&0x1F == 7 {
				sps = nal[1:]
			}
		}
		if sps == nil {
			t.Fatal("no SPS")
		}
		w, h, err := spsSize(sps)
		if err != nil || w != size[0] || h != size[1] {
			t.Fatalf("%dx%d read as %dx%d, %v", size[0], size[1], w, h, err)
		}
	}
}

