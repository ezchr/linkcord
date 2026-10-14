//go:build windows && !no_voice

package golive

import (
	"testing"
	"time"
)

// TestSound captures the sound of all but this test for half a second, as a
// stream of a screen does.
func TestSound(t *testing.T) {
	pcm := make(chan []int16, 64)
	how, stop, err := startSound(Source{Kind: Screen}, pcm)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("capturing", how)
	time.Sleep(500 * time.Millisecond)
	stop()
	samples := 0
	for len(pcm) > 0 {
		samples += len(<-pcm)
	}
	t.Logf("%d samples captured", samples)
}
