//go:build !no_voice

package voice

import "testing"

// frame is 20 ms of sound, at amp on the left and amp*rightPct/100 on the right.
func frame(amp, rightPct int) []int16 {
	pcm := make([]int16, 2*960)
	for i := 0; i < len(pcm); i += 2 {
		v := amp
		if i%4 == 0 {
			v = -amp
		}
		pcm[i], pcm[i+1] = int16(v), int16(v*rightPct/100)
	}
	return pcm
}

func TestFillSilentSide(t *testing.T) {
	var st sideState
	// A microphone on the left only: after a moment, the left plays on both sides.
	var pcm []int16
	for range 100 {
		pcm = frame(3000, 0)
		st.fillSilentSide(pcm)
	}
	if pcm[1] != pcm[0] {
		t.Fatalf("left only: right = %d, left = %d", pcm[1], pcm[0])
	}
	// A little leak into the right at the 1% edge does not flip it back and forth.
	flips, prev := 0, st.copyFrom
	for i := range 200 {
		pcm = frame(3000, 1+i%2)
		st.fillSilentSide(pcm)
		if st.copyFrom != prev {
			flips++
			prev = st.copyFrom
		}
	}
	if flips > 0 {
		t.Fatalf("flipped %d times at the edge", flips)
	}
	// Stereo stays as it is.
	var stereo sideState
	for range 100 {
		pcm = frame(3000, 60)
		stereo.fillSilentSide(pcm)
	}
	if pcm[1] == pcm[0] {
		t.Fatal("stereo was changed")
	}
}
