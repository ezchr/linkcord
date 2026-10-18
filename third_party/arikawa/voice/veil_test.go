package voice

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/thomas-vilte/dave-go/frame"
)

// TestFlipTag checks Linkcord-only voice: a frame with its tag flipped
// fails to decrypt, as on Discord's own clients, and decrypts once flipped
// back, as Linkcord does on receipt.
func TestFlipTag(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)
	opus := []byte("an opus frame of voice, long enough to carry a tag")
	for nonce := uint32(1); nonce < 20; nonce++ {
		f, err := frame.Encrypt(frame.EncryptParams{Plaintext: opus, Key: key, TruncatedNonce: nonce})
		if err != nil {
			t.Fatal(err)
		}
		if !flipTag(f) {
			t.Fatal("could not flip the tag of an encrypted frame")
		}
		if _, _, err := frame.Decrypt(frame.DecryptParams{Ciphertext: f, Key: key}); err == nil {
			t.Fatal("a flipped frame decrypted as Discord's clients decrypt")
		}
		flipTag(f)
		got, _, err := frame.Decrypt(frame.DecryptParams{Ciphertext: f, Key: key})
		if err != nil || !bytes.Equal(got, opus) {
			t.Fatalf("flipped back: %v", err)
		}
	}
	if flipTag([]byte{0xF8, 0xFF, 0xFE}) {
		t.Fatal("flipped the tag of silence, which has none")
	}
}
