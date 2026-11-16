package aec3

import (
	"math"
	"math/rand"
	"testing"
)

// voice is seconds of made up speech, 48 kHz mono: syllables of a buzz with a
// moving pitch through three resonances, or of hiss, with pauses between
// words, as loud as speech. Each seed is another speaker.
func voice(seed int64, seconds float64) []int16 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int16, int(seconds*48000))
	pitch := 90 + 120*r.Float64()
	var phase float64
	var y1, y2 [3]float64
	for at := 0; at < len(out); {
		// A pause after some syllables, longer after some.
		if r.Float64() < 0.3 {
			at += int((0.1 + 0.6*r.Float64()) * 48000)
		}
		n := int((0.08 + 0.2*r.Float64()) * 48000)
		hiss := r.Float64() < 0.2
		f0 := pitch * (0.8 + 0.4*r.Float64())
		formants := [3]float64{300 + 600*r.Float64(), 900 + 1500*r.Float64(), 2500 + 1000*r.Float64()}
		loud := 0.4 + 0.6*r.Float64()
		for i := 0; i < n && at < len(out); i, at = i+1, at+1 {
			var x float64
			if hiss {
				x = (r.Float64()*2 - 1) * 0.3
			} else {
				// A pulse every period of the pitch.
				phase += f0 * (1 + 0.1*float64(i)/float64(n)) / 48000
				if phase >= 1 {
					phase--
					x = 1
				}
			}
			var v float64
			for k, f := range formants {
				c := 2 * math.Exp(-math.Pi*120/48000) * math.Cos(2*math.Pi*f/48000)
				d := -math.Exp(-2 * math.Pi * 120 / 48000)
				y := x + c*y1[k] + d*y2[k]
				y1[k], y2[k] = y, y1[k]
				v += y / float64(k+1)
			}
			env := math.Min(1, math.Min(float64(i), float64(n-i))/480)
			out[at] = int16(math.Max(-30000, math.Min(30000, v*env*loud*900)))
		}
	}
	return out
}

func power(pcm []int16) float64 {
	var sum float64
	for _, v := range pcm {
		sum += float64(v) * float64(v)
	}
	return sum / float64(len(pcm))
}

// The echo of what is played, 50 ms late and half as loud, is removed.
func TestCancellerRemovesEcho(t *testing.T) {
	far := voice(1, 10)
	const delay = 2400
	c := New(1)
	defer c.Close()
	var in, out float64
	mic := make([]int16, FrameSize)
	for at := delay; at+FrameSize <= len(far); at += FrameSize {
		c.Render(far[at : at+FrameSize])
		for i := range mic {
			mic[i] = far[at-delay+i] / 2
		}
		before := power(mic)
		c.Capture(mic)
		if at > 5*48000 {
			in += before
			out += power(mic)
		}
	}
	erle := 10 * math.Log10(in/math.Max(out, 1e-9))
	t.Logf("echo lowered by %.1f dB", erle)
	if erle < 25 {
		t.Fatalf("echo lowered by %.1f dB, want 25 or more", erle)
	}
	_, _, found := c.Metrics()
	t.Logf("delay found: %d ms", found)
}

// With nothing played, what is captured comes out as loud as it went in.
func TestCancellerKeepsVoice(t *testing.T) {
	near := voice(2, 4)
	for _, channels := range []int{1, 2} {
		c := New(channels)
		var in, out float64
		mic := make([]int16, FrameSize*channels)
		for at := 0; at+FrameSize <= len(near); at += FrameSize {
			for i := range FrameSize {
				for ch := range channels {
					mic[i*channels+ch] = near[at+i]
				}
			}
			in += power(mic)
			c.Capture(mic)
			out += power(mic)
		}
		c.Close()
		if change := 10 * math.Log10(out/in); math.Abs(change) > 1 {
			t.Fatalf("%d channels: the voice came out %.2f dB louder, want within 1", channels, change)
		}
	}
}

func benchmark(b *testing.B, channels int) {
	far, near := voice(1, 10), voice(2, 10)
	c := New(channels)
	defer c.Close()
	mic := make([]int16, FrameSize*channels)
	at := 2400
	b.ResetTimer()
	for range b.N {
		if at+FrameSize > len(far) {
			at = 2400
		}
		c.Render(far[at : at+FrameSize])
		for i := range FrameSize {
			v := far[at-2400+i]/2 + near[at+i]/2
			for ch := range channels {
				mic[i*channels+ch] = v
			}
		}
		c.Capture(mic)
		at += FrameSize
	}
}

// One iteration is 10 ms of sound: half a packet.
func BenchmarkCancellerMono(b *testing.B)   { benchmark(b, 1) }
func BenchmarkCancellerStereo(b *testing.B) { benchmark(b, 2) }
