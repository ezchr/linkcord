//go:build !no_voice

package opus

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestStressCall runs encoders and decoders the way a call does, from several
// goroutines at once with garbage collection pressure, lost packets, FEC and
// garbage input.
func TestStressCall(t *testing.T) {
	d := 20 * time.Second
	stop := time.Now().Add(d)
	var wg sync.WaitGroup
	go func() {
		for time.Now().Before(stop) {
			runtime.GC()
			_ = make([]byte, 1<<20)
			time.Sleep(time.Millisecond)
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ch := 1 + g%2
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: ch, Application: VOIP, Bitrate: 64000, Complexity: 10, FEC: true, PacketLoss: 10})
			if err != nil {
				t.Error(err)
				return
			}
			dec, err := NewDecoder(48000, 2)
			if err != nil {
				t.Error(err)
				return
			}
			pcm := make([]int16, 960*ch)
			out := make([]int16, 960*2)
			buf := make([]byte, 1500)
			r := rand.New(rand.NewPCG(uint64(g), 1))
			ph := 0.0
			for i := 0; time.Now().Before(stop); i++ {
				for j := 0; j < 960; j++ {
					v := int16(8000 * math.Sin(ph) * (0.5 + 0.5*math.Sin(float64(i)/7)))
					ph += 0.05 + 0.01*float64(g)
					for c := 0; c < ch; c++ {
						pcm[j*ch+c] = v + int16(r.IntN(200))
					}
				}
				n, err := enc.Encode(pcm, buf)
				if err != nil {
					t.Error(err)
					return
				}
				switch r.IntN(10) {
				case 0:
					dec.Decode(nil, out)
				case 1:
					dec.DecodeFEC(buf[:n], out)
					dec.Decode(buf[:n], out)
				case 2:
					junk := make([]byte, 1+r.IntN(400))
					for k := range junk {
						junk[k] = byte(r.Uint32())
					}
					dec.Decode(junk, out)
				default:
					dec.Decode(buf[:n], out)
				}
			}
		}(g)
	}
	wg.Wait()
}
