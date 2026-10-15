package golive

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/thomas-vilte/dave-go/codecs"
	"github.com/thomas-vilte/dave-go/frame"
)

// TestVeil checks that a veiled picture fails the end-to-end decryption
// Discord's clients do, and decrypts once Linkcord unveils it.
func TestVeil(t *testing.T) {
	body := make([]byte, 500)
	rand.Read(body)
	for i := range body {
		if body[i] == 0 {
			body[i] = 1
		}
	}
	plain := append([]byte{0, 0, 0, 1, 0x65, 0x88, 0x80}, body...)
	normal, ranges, err := codecs.H264Frame(plain)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 16)
	rand.Read(key)
	for nonce := uint32(1); nonce < 40; nonce++ {
		pic, err := frame.Encrypt(frame.EncryptParams{Plaintext: normal, Key: key, TruncatedNonce: nonce, UnencryptedRanges: ranges})
		if err != nil {
			t.Fatal(err)
		}
		if !veil(pic) {
			continue
		}
		if _, _, err := frame.Decrypt(frame.DecryptParams{Ciphertext: pic, Key: key}); err == nil {
			t.Fatal("a veiled picture decrypted as it is")
		}
		got, _, err := frame.Decrypt(frame.DecryptParams{Ciphertext: unveiled(pic), Key: key})
		if err != nil {
			t.Fatal("an unveiled picture did not decrypt:", err)
		}
		if !bytes.Equal(got, normal) {
			t.Fatal("the unveiled picture is not the one encrypted")
		}
	}
}
