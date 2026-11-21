//go:build !no_voice

package voice

import (
	"math"
	"testing"
)

// A mono microphone (two equal channels) comes out as two equal channels,
// RNNoise having run on one.
func TestProcessMonoMic(t *testing.T) {
	p := newProcessor()
	pcm := make([]int16, 48000/100*channels*10) // 100 ms
	for i := 0; i < len(pcm)/channels; i++ {
		v := int16(8000 * math.Sin(float64(i)*2*math.Pi*440/48000))
		pcm[i*channels], pcm[i*channels+1] = v, v
	}
	p.process(pcm)
	for i := 0; i < len(pcm); i += channels {
		if pcm[i] != pcm[i+1] {
			t.Fatalf("sample %d: %d and %d", i/channels, pcm[i], pcm[i+1])
		}
	}
	if p.ns[1] != nil {
		t.Fatal("RNNoise ran on the second channel too")
	}
}
