package voice

import (
	"sync/atomic"

	"github.com/thomas-vilte/dave-go/frame"
)

// Voice for Linkcord only is end-to-end encrypted as any other, with the
// authentication tag of each frame flipped by veilMask: Discord's own clients
// find every frame forged and play nothing, while Linkcord flips it back.
var veilMask = [8]byte{'L', 'i', 'n', 'k', 'c', 'o', 'r', 'd'}

// flipTag flips the tag of an end-to-end encrypted frame in place, and
// reports whether it is one.
func flipTag(f []byte) bool {
	parsed, err := frame.Parse(f)
	if err != nil {
		return false
	}
	at := len(f) - int(parsed.SupplementalSize)
	if at < 0 || at+len(veilMask) > len(f) {
		return false
	}
	for i := range veilMask {
		f[at+i] ^= veilMask[i]
	}
	return true
}

// veiled is set while what this session sends is for Linkcord only.
type veiled struct{ on atomic.Bool }

// SetLinkcordOnly veils what is sent from now on, or stops.
func (s *Session) SetLinkcordOnly(on bool) { s.veil.on.Store(on) }
