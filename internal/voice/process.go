//go:build !no_voice

package voice

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/ezchr/linkcord/third_party/rnnoise"
)

const (
	// blockSamples is how many samples, of both channels, the processor
	// works on at a time: 10 ms, as RNNoise does.
	blockSamples = rnnoise.FrameSize * channels

	// agcTarget is how loud, in RMS, the automatic gain control makes what
	// is said: -18 dBFS, as loud as Discord sends speech.
	agcTarget = 0.125 * math.MaxInt16
	// agcSilence is how loud what is captured must be for the automatic gain
	// control to follow it, so that it does not make silence louder.
	agcSilence = 0.003 * math.MaxInt16
	// agcMin and agcMax are how much it lowers and raises at most.
	agcMin, agcMax = 0.25, 6
	// limit is how loud a sample may be, a little below clipping, which
	// sounds broken up.
	limit = 0.9 * math.MaxInt16
)

// processor readies what the microphone captures to be sent: as loud as the
// input volume says, without noise if asked, evened out by an automatic
// gain control if asked, and kept from clipping. It is used from one
// goroutine at a time.
type processor struct {
	// gain is the input volume, as the bits of a float32.
	gain              atomic.Uint32
	denoise, autoGain atomic.Bool

	mu sync.Mutex
	// ns removes the noise of each channel, made when first needed.
	ns [channels]*rnnoise.State
	// agc and lim are the gain of the automatic gain control and of the
	// limiter, which change smoothly from block to block.
	agc, lim float32
	buf      [channels][rnnoise.FrameSize]float32
}

func newProcessor() *processor {
	p := &processor{agc: 1, lim: 1}
	p.gain.Store(math.Float32bits(1))
	p.denoise.Store(true)
	p.autoGain.Store(true)
	return p
}

// process processes pcm, 48 kHz stereo samples, in place, in blocks of
// 10 ms; what is left over a whole block is only made as loud as the input
// volume says.
func (p *processor) process(pcm []int16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	gain := math.Float32frombits(p.gain.Load())
	denoise, autoGain := p.denoise.Load(), p.autoGain.Load()
	if !denoise {
		p.closeLocked()
	}
	n := len(pcm) / blockSamples * blockSamples
	for at := 0; at < n; at += blockSamples {
		p.block(pcm[at:at+blockSamples], gain, denoise, autoGain)
	}
	for i := n; i < len(pcm); i++ {
		pcm[i] = amplify(pcm[i], gain)
	}
}

// block processes 10 ms of samples.
func (p *processor) block(pcm []int16, gain float32, denoise, autoGain bool) {
	for i, v := range pcm {
		p.buf[i%channels][i/channels] = float32(v) * gain
	}
	if denoise {
		for c := range channels {
			if p.ns[c] == nil {
				p.ns[c] = rnnoise.New()
			}
			p.ns[c].Process(p.buf[c][:])
		}
	}

	// The automatic gain control follows what is said quickly when it gets
	// louder, and slowly, over a second or so, when it gets quieter.
	var sum, peak float64
	for c := range channels {
		for _, v := range p.buf[c] {
			sum += float64(v) * float64(v)
			peak = max(peak, math.Abs(float64(v)))
		}
	}
	rms := math.Sqrt(sum / blockSamples)
	start := p.agc
	if !autoGain {
		p.agc = 1
	} else if rms > agcSilence {
		want := float32(min(max(agcTarget/rms, agcMin), agcMax))
		if want < p.agc {
			p.agc += (want - p.agc) * 0.3
		} else {
			p.agc += (want - p.agc) * 0.01
		}
	}

	// The limiter lowers what would clip at once, and lets go over 100 ms.
	startLim := p.lim
	if peaked := float32(peak) * p.agc; peaked*p.lim > limit {
		p.lim = limit / peaked
	} else {
		p.lim = min(1, p.lim+0.1)
	}

	// Gains change across the block, rather than at once, which clicks.
	frames := rnnoise.FrameSize
	for i := range frames {
		t := float32(i+1) / float32(frames)
		g := (start + (p.agc-start)*t) * min(startLim, p.lim) // never louder than either limit
		for c := range channels {
			v := p.buf[c][i] * g
			pcm[i*channels+c] = int16(max(-limit, min(limit, v)))
		}
	}
}

// close frees what removes the noise.
func (p *processor) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *processor) closeLocked() {
	for c, ns := range p.ns {
		if ns != nil {
			ns.Close()
			p.ns[c] = nil
		}
	}
}
