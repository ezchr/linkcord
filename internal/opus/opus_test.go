//go:build !no_voice

package opus

import (
	"math"
	"testing"
)

// TestRoundTrip encodes a tone and decodes it back, through a lost packet
// made up from the next one's FEC.
func TestRoundTrip(t *testing.T) {
	for _, app := range []Application{VOIP, Audio} {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: app, Bitrate: 64000, FEC: true, PacketLoss: 10})
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(48000, 2)
		if err != nil {
			t.Fatal(err)
		}
		pcm := make([]int16, 960*2)
		out := make([]int16, 5760*2)
		packet := make([]byte, 1500)
		var energy float64
		for frame := range 50 {
			for i := range 960 {
				v := int16(8000 * math.Sin(2*math.Pi*440*float64(frame*960+i)/48000))
				pcm[2*i], pcm[2*i+1] = v, v
			}
			n, err := enc.Encode(pcm, packet)
			if err != nil || n < 2 {
				t.Fatal(n, err)
			}
			if frame == 20 {
				continue
			}
			if frame == 21 {
				if got, err := dec.DecodeFEC(packet[:n], out[:960*2]); err != nil || got != 960 {
					t.Fatal(got, err)
				}
			}
			got, err := dec.Decode(packet[:n], out)
			if err != nil || got != 960 {
				t.Fatal(got, err)
			}
			if frame > 40 {
				for _, v := range out[:got*2] {
					energy += float64(v) * float64(v)
				}
			}
		}
		if rms := math.Sqrt(energy / (9 * 960 * 2)); rms < 3000 {
			t.Fatalf("application %d: decoded to %f RMS, want about 5657", app, rms)
		}
		if got, err := dec.Decode(nil, out[:960*2]); err != nil || got != 960 {
			t.Fatal(got, err)
		}
	}
	t.Log(Version())
}

func TestBadArgs(t *testing.T) {
	if _, err := NewEncoder(EncoderConfig{SampleRate: 44100, Channels: 2}); err == nil {
		t.Fatal("44.1 kHz encoder made")
	}
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Encode(make([]int16, 1000), make([]byte, 100)); err == nil {
		t.Fatal("frame of no Opus length encoded")
	}
	dec, _ := NewDecoder(48000, 2)
	if _, err := dec.Decode([]byte{0xff, 0xff, 0xff}, make([]int16, 5760*2)); err == nil {
		t.Fatal("bad packet decoded")
	}
}
