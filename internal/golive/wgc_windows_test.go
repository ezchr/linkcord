//go:build windows && !no_voice

package golive

import (
	"os"
	"testing"
	"time"
)

// TestGPUCaptureRate takes pictures for three seconds and reports how they
// were taken and how many were new. Set LINKCORD_NO_WGC to compare.
func TestGPUCaptureRate(t *testing.T) {
	src := Sources(0, 160)
	if len(src) == 0 {
		t.Skip("nothing to capture")
	}
	g := newGPUCapture(src[0], true)
	if g == nil {
		t.Skip("no graphics card capture")
	}
	defer g.close()
	var pictures, fresh int
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		<-tick.C
		w, h, status, changed, _ := g.grab()
		if status == gpuNever {
			t.Fatal("the graphics card cannot capture")
		}
		if status == gpuPicture {
			pictures++
			if changed {
				fresh++
			}
			if pictures == 1 {
				if _, _, err := g.convert(w, h); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Logf("%s: %d pictures, %d new, wgc off=%q", g.method(), pictures, fresh, os.Getenv("LINKCORD_NO_WGC"))
	if pictures == 0 {
		t.Fatal("no pictures")
	}
}
