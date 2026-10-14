package rnnoise

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestNoise has RNNoise quieten white noise, which holds no speech.
func TestNoise(t *testing.T) {
	s := New()
	defer s.Close()
	r := rand.New(rand.NewPCG(1, 2))
	var in, out float64
	frame := make([]float32, FrameSize)
	for n := range 200 {
		for i := range frame {
			frame[i] = float32(r.NormFloat64() * 2000)
			if n >= 100 {
				in += float64(frame[i]) * float64(frame[i])
			}
		}
		s.Process(frame)
		if n >= 100 {
			for _, v := range frame {
				out += float64(v) * float64(v)
			}
		}
	}
	db := 10 * math.Log10(out/in)
	t.Logf("noise lowered by %.1f dB", -db)
	if db > -5 {
		t.Errorf("noise lowered by %.1f dB only", -db)
	}
}
