package session

// Added by Linkcord: end-to-end encryption of the H264 frames of a screen
// share, which upstream leaves out on purpose.

import (
	"errors"
	"fmt"

	"github.com/thomas-vilte/dave-go/codecs"
	"github.com/thomas-vilte/dave-go/frame"
)

// maxH264Attempts is how many nonces EncryptH264 tries before it gives up
// on a frame whose encryption keeps holding a start code.
const maxH264Attempts = 10

// ErrStartCode is returned by EncryptH264 when no nonce it tried gave a
// frame free of start codes; the frame should be dropped.
var ErrStartCode = errors.New("dave: encrypted H264 frame kept holding a start code")

// EncryptH264 encrypts f, an H264 access unit in Annex B, as libdave does,
// and returns it with the DAVE footer. Without keys yet, it returns the
// frame as it is, which receivers pass through.
//
// Unlike upstream's H26x path, every attempt takes a fresh nonce from the
// send counter, so a retry never reuses one.
func (s *Session) EncryptH264(f []byte) ([]byte, error) {
	plain, ranges, err := codecs.H264Frame(f)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ratchet := s.selectSendRatchetLocked()
	if ratchet == nil {
		s.stats.PassthroughFrames++

		return plain, nil
	}

	// The footer grows with the readable ranges, two varints each, which
	// MaxEncryptedFrameSize does not count.
	out := make([]byte, s.MaxEncryptedFrameSize(len(plain))+20*len(ranges))
	for range maxH264Attempts {
		_, truncatedNonce, generation := s.sendCounter.Next()
		key, err := ratchet.GetKey(generation)
		if err != nil {
			s.stats.EncryptFailures++

			return nil, err
		}
		ratchet.Commit(generation)

		n, err := frame.EncryptInto(out, frame.EncryptParams{
			Plaintext:         plain,
			Key:               key,
			TruncatedNonce:    truncatedNonce,
			UnencryptedRanges: ranges,
		})
		if err != nil {
			s.stats.EncryptFailures++

			return nil, fmt.Errorf("dave: encrypt H264: %w", err)
		}

		// Start codes may stay where the frame is readable, but neither in
		// what was encrypted nor in the footer.
		cipher := frame.ExtractCiphertext(out[:len(plain)], ranges)
		if !codecs.HasStartCode(cipher) && !codecs.HasStartCode(out[len(plain):n]) {
			return out[:n], nil
		}
	}
	s.stats.EncryptFailures++

	return nil, ErrStartCode
}
