//go:build !no_voice

package voice

import "testing"

func TestCenterMono(t *testing.T) {
	// A microphone on the left only: the left goes to both, at full volume.
	s := []int16{1000, 0, -800, 0, 600, 1}
	centerMono(s)
	if s[0] != 1000 || s[1] != 1000 || s[2] != -800 || s[3] != -800 {
		t.Fatalf("left-only microphone = %v; want it on both sides", s)
	}
	// A real stereo signal: averaged.
	s = []int16{1000, 600, -400, -200}
	centerMono(s)
	if s[0] != 800 || s[1] != 800 || s[2] != -300 || s[3] != -300 {
		t.Fatalf("stereo = %v; want averaged", s)
	}
	// The right only.
	s = []int16{0, 500, 0, -500}
	centerMono(s)
	if s[0] != 500 || s[1] != 500 {
		t.Fatalf("right-only microphone = %v", s)
	}
}
