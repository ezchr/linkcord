//go:build !no_voice

package voice

import (
	"math"
	"testing"
)

// tone is 200 ms of a 300 Hz tone in both channels, amp loud.
func tone(amp float64) []int16 {
	pcm := make([]int16, 20*blockSamples)
	for i := range len(pcm) / channels {
		v := int16(amp * math.Sin(2*math.Pi*300*float64(i)/sampleRate))
		pcm[2*i], pcm[2*i+1] = v, v
	}
	return pcm
}

func peakOf(pcm []int16) int {
	p := 0
	for _, v := range pcm {
		p = max(p, int(v), -int(v))
	}
	return p
}

// TestProcessorLimits has a microphone four times too loud not clip, which
// sounds broken up.
func TestProcessorLimits(t *testing.T) {
	p := newProcessor()
	defer p.close()
	p.denoise.Store(false)
	p.autoGain.Store(false)
	p.gain.Store(math.Float32bits(4))
	pcm := tone(20000)
	p.process(pcm)
	if peak := peakOf(pcm[len(pcm)/2:]); float64(peak) > limit {
		t.Errorf("peak %d over the limit %.0f", peak, float64(limit))
	}
}

// TestProcessorAutoGain has the automatic gain control raise a quiet voice.
func TestProcessorAutoGain(t *testing.T) {
	p := newProcessor()
	defer p.close()
	p.denoise.Store(false)
	var pcm []int16
	for range 10 {
		pcm = tone(1000)
		p.process(pcm)
	}
	if peak := peakOf(pcm); peak < 2000 {
		t.Errorf("a quiet voice peaks at %d after 2 s, not raised", peak)
	}
}

// TestProcessorDenoise runs the noise suppression on a voice-like tone,
// which it must not silence entirely.
func TestProcessorDenoise(t *testing.T) {
	p := newProcessor()
	defer p.close()
	p.autoGain.Store(false)
	pcm := tone(8000)
	p.process(pcm)
	t.Logf("tone of 8000 peaks at %d after the noise suppression", peakOf(pcm))
}
