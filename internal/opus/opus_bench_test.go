//go:build !no_voice

package opus

import (
	"fmt"
	"math"
	"testing"
)

// BenchmarkEncode runs what encoding a 20 ms stereo frame costs with each
// of a few configurations, on a made up voice: a tone gliding up and down
// with a little noise.
func BenchmarkEncode(b *testing.B) {
	frames := make([][]int16, 50)
	seed := uint32(1)
	for f := range frames {
		frames[f] = make([]int16, 960*2)
		for i := range 960 {
			t := float64(f*960+i) / 48000
			seed = seed*1664525 + 1013904223
			v := int16(6000*math.Sin(2*math.Pi*(150+60*math.Sin(3*t))*t) + float64(int32(seed)>>20))
			frames[f][2*i], frames[f][2*i+1] = v, v
		}
	}
	for _, cfg := range []EncoderConfig{
		{Application: VOIP, Bitrate: 64000, Complexity: 10, FEC: true, PacketLoss: 10},
		{Application: VOIP, Bitrate: 64000, Complexity: 10},
		{Application: VOIP, Bitrate: 64000, Complexity: 5, FEC: true, PacketLoss: 10},
		{Application: Audio, Bitrate: 64000, Complexity: 10},
		{Application: Audio, Bitrate: 128000, Complexity: 10},
	} {
		cfg.SampleRate, cfg.Channels = 48000, 2
		name := fmt.Sprintf("app=%d/%dk/c%d/fec=%v", cfg.Application, cfg.Bitrate/1000, cfg.Complexity, cfg.FEC)
		b.Run(name, func(b *testing.B) {
			enc, err := NewEncoder(cfg)
			if err != nil {
				b.Fatal(err)
			}
			packet := make([]byte, 1500)
			b.ReportAllocs()
			for n := range b.N {
				if _, err := enc.Encode(frames[n%len(frames)], packet); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
