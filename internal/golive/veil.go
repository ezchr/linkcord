package golive

import "github.com/thomas-vilte/dave-go/frame"

// A stream for Linkcord only is end-to-end encrypted as any other, with the
// authentication tag of each picture flipped by veilMask: Discord's own
// clients then find every picture forged and show none, while Linkcord
// flips it back.
var veilMask = [8]byte{'L', 'i', 'n', 'k', 'c', 'o', 'r', 'd'}

// veil flips the tag of an end-to-end encrypted picture in place. It reports
// false if the picture is not one, or if the flipped tag reads as a start
// code, which would split the picture: it is then encrypted again.
func veil(pic []byte) bool {
	parsed, err := frame.Parse(pic)
	if err != nil {
		return false
	}
	at := len(pic) - int(parsed.SupplementalSize)
	for i := range veilMask {
		pic[at+i] ^= veilMask[i]
	}
	for i := max(at-2, 0); i < at+8; i++ {
		if pic[i] == 0 && pic[i+1] == 0 && pic[i+2] <= 3 {
			return false
		}
	}
	return true
}

// unveiled is a copy of pic with its tag flipped back.
func unveiled(pic []byte) []byte {
	out := append([]byte(nil), pic...)
	parsed, err := frame.Parse(out)
	if err != nil {
		return pic
	}
	at := len(out) - int(parsed.SupplementalSize)
	for i := range veilMask {
		out[at+i] ^= veilMask[i]
	}
	return out
}
