//go:build !no_voice

package voice

import "testing"

func TestFillSilentSide(t *testing.T) {
	// Sound on the left only: copied to the right.
	pcm := []int16{3000, 0, -2500, 0, 2000, 1, -1500, 0}
	fillSilentSide(pcm)
	if pcm[1] != 3000 || pcm[3] != -2500 || pcm[5] != 2000 {
		t.Fatalf("left only = %v; want it on both sides", pcm)
	}
	// Stereo stays as it is, with nothing averaged.
	pcm = []int16{3000, 1000, -2500, -800}
	fillSilentSide(pcm)
	if pcm[0] != 3000 || pcm[1] != 1000 || pcm[2] != -2500 || pcm[3] != -800 {
		t.Fatalf("stereo changed: %v", pcm)
	}
	// Near silence on both sides is left alone.
	pcm = []int16{3, 0, -2, 0}
	fillSilentSide(pcm)
	if pcm[1] != 0 || pcm[3] != 0 {
		t.Fatalf("silence changed: %v", pcm)
	}
	// The right only.
	pcm = []int16{0, 1200, 0, -1200}
	fillSilentSide(pcm)
	if pcm[0] != 1200 || pcm[2] != -1200 {
		t.Fatalf("right only = %v", pcm)
	}
}
