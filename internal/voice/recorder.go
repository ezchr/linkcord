//go:build !no_voice

package voice

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/go-opus/opus"
)

// maxRecording is how long a voice message can be, as Discord's.
const maxRecording = 20 * time.Minute

// Recorder records the microphone for a voice message, made as loud and as
// clean as what is said in calls.
type Recorder struct {
	device  *malgo.Device
	proc    *processor
	mu      sync.Mutex
	pending []int16
	pcm     []int16 // 48 kHz stereo
	peak    int
	started time.Time
}

// RecordOptions are how what is recorded is processed, as in calls.
type RecordOptions struct {
	Input          string
	Gain, Strength float32
	Denoise, AGC   bool
}

// StartRecorder starts recording the microphone o.Input, "" for the
// default one.
func StartRecorder(o RecordOptions) (*Recorder, error) {
	r := &Recorder{proc: newProcessor(), started: time.Now()}
	r.proc.gain.Store(math.Float32bits(o.Gain))
	r.proc.strength.Store(math.Float32bits(o.Strength))
	r.proc.denoise.Store(o.Denoise)
	r.proc.autoGain.Store(o.AGC)
	device, err := startDeviceOn(malgo.Capture, o.Input, channels, r.captured)
	if err != nil {
		return nil, err
	}
	r.device = device
	return r, nil
}

func (r *Recorder) captured(_, input []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pcm) >= int(maxRecording.Seconds())*sampleRate*channels {
		return
	}
	for i := 0; i+1 < len(input); i += 2 {
		r.pending = append(r.pending, int16(binary.LittleEndian.Uint16(input[i:])))
	}
	n := len(r.pending) / blockSamples * blockSamples
	if n == 0 {
		return
	}
	samples := r.pending[:n]
	r.proc.process(samples)
	peak := 0
	for _, v := range samples {
		peak = max(peak, int(v), -int(v))
	}
	r.peak = peak
	r.pcm = append(r.pcm, samples...)
	r.pending = append(r.pending[:0], r.pending[n:]...)
}

// Elapsed is how long has been recorded.
func (r *Recorder) Elapsed() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Duration(len(r.pcm)/channels) * time.Second / sampleRate
}

// Level is how loud the microphone is now, from 0 to 1.
func (r *Recorder) Level() float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return float32(r.peak) / 32767
}

// Stop stops recording, and returns what was recorded, 48 kHz stereo.
func (r *Recorder) Stop() []int16 {
	r.device.Uninit()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pcm
}

// VoiceMessage is a recording as Discord takes a voice message: an Ogg
// Opus file, mono, how long it lasts and its waveform, a byte of loudness
// for each of up to 256 stretches.
func VoiceMessage(stereo []int16) (ogg []byte, seconds float64, waveform []byte, err error) {
	const frame = sampleRate / 50 // 20ms
	mono := make([]int16, len(stereo)/2)
	for i := range mono {
		mono[i] = int16((int32(stereo[2*i]) + int32(stereo[2*i+1])) / 2)
	}
	if len(mono) < frame {
		return nil, 0, nil, errors.New("the recording is too short")
	}
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: sampleRate, Channels: 1})
	if err != nil {
		return nil, 0, nil, err
	}
	var packets [][]byte
	buf := make([]byte, 4000)
	pcm := make([]int16, frame)
	for at := 0; at < len(mono); at += frame {
		clear(pcm)
		copy(pcm, mono[at:])
		n, err := encoder.Encode(pcm, buf)
		if err != nil {
			return nil, 0, nil, err
		}
		packets = append(packets, append([]byte(nil), buf[:n]...))
	}
	seconds = float64(len(mono)) / sampleRate
	return oggOpus(packets, frame), seconds, waveformOf(mono), nil
}

// waveformOf is the loudness of up to 256 stretches of pcm, from 0 to 255.
func waveformOf(pcm []int16) []byte {
	n := min(256, max(1, len(pcm)/(sampleRate/10)))
	out := make([]byte, n)
	per := len(pcm) / n
	for i := range out {
		var sum float64
		for _, v := range pcm[i*per : (i+1)*per] {
			sum += float64(v) * float64(v)
		}
		rms := math.Sqrt(sum/float64(max(per, 1))) / 32767
		// Louder shows taller, as Discord's, quiet speech still visible.
		out[i] = byte(min(255, math.Sqrt(rms)*255*1.4))
	}
	return out
}

// oggOpus puts Opus packets of frame samples each, mono at 48 kHz, in an
// Ogg file, as RFC 7845 says.
func oggOpus(packets [][]byte, frame int) []byte {
	const preSkip = 312
	serial := uint32(time.Now().UnixNano())
	var out []byte
	seq := uint32(0)
	page := func(flags byte, granule int64, segs [][]byte) {
		var table []byte
		var body []byte
		for _, p := range segs {
			n := len(p)
			for n >= 255 {
				table = append(table, 255)
				n -= 255
			}
			table = append(table, byte(n))
			body = append(body, p...)
		}
		h := make([]byte, 0, 27+len(table)+len(body))
		h = append(h, "OggS"...)
		h = append(h, 0, flags)
		h = binary.LittleEndian.AppendUint64(h, uint64(granule))
		h = binary.LittleEndian.AppendUint32(h, serial)
		h = binary.LittleEndian.AppendUint32(h, seq)
		h = binary.LittleEndian.AppendUint32(h, 0) // checksum, below
		h = append(h, byte(len(table)))
		h = append(h, table...)
		h = append(h, body...)
		binary.LittleEndian.PutUint32(h[22:], oggCRC(h))
		out = append(out, h...)
		seq++
	}
	head := []byte("OpusHead")
	head = append(head, 1, 1)
	head = binary.LittleEndian.AppendUint16(head, preSkip)
	head = binary.LittleEndian.AppendUint32(head, sampleRate)
	head = append(head, 0, 0, 0)
	page(0x02, 0, [][]byte{head})
	tags := []byte("OpusTags")
	tags = binary.LittleEndian.AppendUint32(tags, 8)
	tags = append(tags, "linkcord"...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	page(0, 0, [][]byte{tags})
	// A page holds a second of packets, fewer than 255 segments.
	const perPage = 50
	for i := 0; i < len(packets); i += perPage {
		end := min(i+perPage, len(packets))
		flags := byte(0)
		if end == len(packets) {
			flags = 0x04
		}
		page(flags, int64(preSkip+end*frame), packets[i:end])
	}
	return out
}

var oggTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return
}()

// oggCRC is the checksum of an Ogg page.
func oggCRC(b []byte) uint32 {
	var crc uint32
	for _, v := range b {
		crc = crc<<8 ^ oggTable[byte(crc>>24)^v]
	}
	return crc
}
