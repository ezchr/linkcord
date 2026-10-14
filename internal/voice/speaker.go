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
	// sides plays sound sent on one side only on both.
	sides sideState
	pcm   []int16
	buf   []byte
	// playing is whether the cushion was buffered and what is left of it is being played.
	playing bool
	// seq is the sequence number of the last packet, and last when it arrived.
	seq  uint16
	last time.Time
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

	s.sides.fillSilentSide(pcm[:n*channels])

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

// mix adds what is buffered to output, volume times as loud, once the cushion is.
func (s *speaker) mix(output []byte, volume float32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.playing = s.playing || len(s.buf) >= cushion; !s.playing {
		return
	}

	n := min(len(output), len(s.buf))
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

// sideState follows which side of someone's sound is silent, slowly, so that the choice
// does not flip from one 20 ms frame to the next, which crackles.
type sideState struct {
	left, right float64 // the sides' loudness, averaged over about a second
	copyFrom    int     // 0 when both sides play as sent, 1 to copy the left, 2 the right
}

// fillSilentSide plays a sound sent on one side only on both sides, as when someone's
// microphone gives sound on the left alone: the silent side gets a copy of the other.
// Nothing is mixed, so stereo sound is left as it is.
func (st *sideState) fillSilentSide(pcm []int16) {
	var left, right int64
	for i := 0; i+1 < len(pcm); i += 2 {
		left += abs64(int64(pcm[i]))
		right += abs64(int64(pcm[i+1]))
	}
	frames := float64(max(len(pcm)/2, 1))
	l, r := float64(left)/frames, float64(right)/frames
	// Only frames with sound teach which side is silent; pauses change nothing.
	if l > 50 || r > 50 {
		const k = 0.05 // about a second of 20 ms frames
		st.left += k * (l - st.left)
		st.right += k * (r - st.right)
	}
	// A side is silent under 1% of the other, and stays so until over 5%.
	switch st.copyFrom {
	case 0:
		if st.right*100 < st.left && st.left > 50 {
			st.copyFrom = 1
		} else if st.left*100 < st.right && st.right > 50 {
			st.copyFrom = 2
		}
	case 1:
		if st.right*20 > st.left {
			st.copyFrom = 0
		}
	case 2:
		if st.left*20 > st.right {
			st.copyFrom = 0
		}
	}
	switch st.copyFrom {
	case 1:
		for i := 0; i+1 < len(pcm); i += 2 {
			pcm[i+1] = pcm[i]
		}
	case 2:
		for i := 0; i+1 < len(pcm); i += 2 {
			pcm[i] = pcm[i+1]
		}
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
