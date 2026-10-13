package codecs

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/thomas-vilte/dave-go/frame"
)

func TestH264Frame(t *testing.T) {
	sps := []byte{0x67, 0x42, 0xc0, 0x1f, 0xda}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	// An IDR slice: first_mb_in_slice 0 ("1"), slice_type 7 ("0001000"),
	// pic_parameter_set_id 0 ("1"): 9 bits, so 2 bytes cover them.
	idr := []byte{0x65, 0x88, 0x80, 0x11, 0x22, 0x33, 0x44}
	in := append(append(append(append(append([]byte{0, 0, 1}, sps...), 0, 0, 0, 1), pps...), 0, 0, 1), idr...)

	out, ranges, err := H264Frame(in)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Join([][]byte{nil, sps, pps, idr}, longStartCode)
	if !bytes.Equal(out, want) {
		t.Fatalf("frame = % x, want % x", out, want)
	}
	// The SPS, the PPS, the IDR's start code, header and 2 bytes of slice
	// header are one readable range; the rest of the slice is encrypted.
	clear := 4 + len(sps) + 4 + len(pps) + 4 + 1 + 2
	if len(ranges) != 1 || ranges[0] != (frame.Range{Offset: 0, Length: clear}) {
		t.Fatalf("ranges = %v, want [{0 %d}]", ranges, clear)
	}

	key := make([]byte, 16)
	rand.Read(key)
	enc, err := frame.Encrypt(frame.EncryptParams{Plaintext: out, Key: key, TruncatedNonce: 7, UnencryptedRanges: ranges})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc[:clear], out[:clear]) {
		t.Fatal("the readable part was changed")
	}
	dec, nonce, err := frame.Decrypt(frame.DecryptParams{Ciphertext: enc, Key: key})
	if err != nil || nonce != 7 || !bytes.Equal(dec, out) {
		t.Fatalf("decrypt = % x, %d, %v", dec, nonce, err)
	}
}

func TestBytesCoveringH264PPSSkipsEmulationPrevention(t *testing.T) {
	// first_mb_in_slice with 16 leading zeros, written with an emulation
	// prevention byte after the two zero bytes.
	p := []byte{0x00, 0x00, 0x03, 0x80, 0x01, 0xe0}
	// 16 zeros (2 bytes), skip the 0x03 byte, then "1" + 16 bits of value
	// end at bit 8*3+17 = 41 of the bytes, then "1" (slice_type) and "1"
	// (pps) at bits 41 and 42: 43 bits, so 43/8+1 = 6 bytes.
	if n := bytesCoveringH264PPS(p); n != 6 {
		t.Fatalf("got %d, want 6", n)
	}
}
