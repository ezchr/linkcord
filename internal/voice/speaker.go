//go:build !no_voice

package voice

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/tphakala/go-opus/opus"
)

const (
	sampleRate = 48000
	channels   = 2
	// frameSamples and frameBytes are the sizes of 20ms of PCM.
	frameSamples = sampleRate / 50 * channels
	frameBytes   = frameSamples * 2

	// cushion is how much is buffered before it is played, so that packets arriving unevenly do not make gaps.
	cushion = 3 * frameBytes
	// maxBuffered is how much is buffered before the oldest is dropped rather than fall behind.
	maxBuffered = 10 * frameBytes
	// maxLost is how many packets in a row are made up for.
	maxLost = 3
)

// speaker decodes and buffers what one user says, to be mixed into what is played.
type speaker struct {
	// user is who speaks, once the voice server said. It is read and written with the session's state locked.
	user discord.UserID

	mu      sync.Mutex
	decoder *opus.Decoder
	pcm     []int16
	buf     []byte
	// playing is whether the cushion was buffered and what is left of it is being played.
	playing bool
	// seq is the sequence number of the last packet, and last when it arrived.
	seq  uint16
	last time.Time
	// guard lowers the voice when it comes in saturated.
	guard levelGuard
}

// newSpeaker returns a speaker whose first packet has the sequence number seq.
func newSpeaker(seq uint16) (*speaker, error) {
	decoder, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	// 120ms is the longest an Opus packet can be.
	return &speaker{decoder: decoder, pcm: make([]int16, 6*frameSamples), seq: seq - 1}, nil
}

// decode appends what packet decodes to, or a made up frame if it is nil.
func (s *speaker) decode(packet []byte) error {
	pcm := s.pcm
	if packet == nil {
		pcm = pcm[:frameSamples]
	}

	n, err := s.decoder.Decode(packet, pcm)
	if err != nil {
		return err
	}

	for _, sample := range pcm[:n*channels] {
		s.buf = binary.LittleEndian.AppendUint16(s.buf, uint16(sample))
	}

	return nil
}

// write buffers the packet with the sequence number seq.
func (s *speaker) write(seq uint16, packet []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make up for the few packets that never arrived. One that arrives late or twice is not worth playing.
	gap := seq - s.seq
	if gap == 0 || gap > 1<<15 && time.Since(s.last) < time.Second {
		return nil
	}

	if gap > 1 && gap <= maxLost+1 {
		for range gap - 1 {
			if err := s.decode(nil); err != nil {
				return err
			}
		}
	}

	s.seq, s.last = seq, time.Now()

	if err := s.decode(packet); err != nil {
		return err
	}

	if len(s.buf) > maxBuffered {
		s.buf = s.buf[len(s.buf)-cushion:]
	}

	return nil
}

// mix adds what is buffered to output, volume times as loud, once the
// cushion is, lowered when it comes in saturated if guard.
func (s *speaker) mix(output []byte, volume float32, guard bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.playing = s.playing || len(s.buf) >= cushion; !s.playing {
		return
	}

	n := min(len(output), len(s.buf))
	if guard {
		var peak float32
		for i := 0; i+1 < n; i += 2 {
			v := float32(int16(binary.LittleEndian.Uint16(s.buf[i:])))
			peak = max(peak, v, -v)
		}
		volume *= s.guard.apply(peak)
	}
	for i := 0; i+1 < n; i += 2 {
		sample := int32(int16(binary.LittleEndian.Uint16(s.buf[i:])))
		if volume != 1 {
			sample = int32(float32(sample) * volume)
		}

		sum := int32(int16(binary.LittleEndian.Uint16(output[i:]))) + sample
		binary.LittleEndian.PutUint16(output[i:], uint16(max(-1<<15, min(1<<15-1, sum))))
	}

	s.buf = s.buf[n:]
	s.playing = len(s.buf) > 0
}
