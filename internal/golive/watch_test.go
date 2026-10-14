//go:build windows && !no_voice

package golive

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
)

// lockedBuffer stands for the decoder's input.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) Close() error { return nil }
func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.b.Bytes())
}

// TestWatchOpen checks that only media bytes survive transport decryption,
// including when an extension and RTP padding surround the DAVE footer.
func TestWatchOpen(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			m := &media{}
			if err := m.useSecret(mode, [32]byte{}); err != nil {
				t.Fatal(err)
			}
			w := &Watch{aead: m.aead}
			want := []byte{0x65, 0x88, 0x80, 0x11, 0xfa, 0xfa}
			for _, tc := range []struct {
				name    string
				ext     bool
				padded  bool
				padding []byte
				bad     bool
			}{
				{name: "plain"},
				{name: "extension", ext: true},
				{name: "padding", padded: true, padding: []byte{0, 0, 0, 4}},
				{name: "extension and padding", ext: true, padded: true, padding: []byte{0, 0, 0, 4}},
				{name: "zero padding count", padded: true, padding: []byte{0}, bad: true},
				{name: "oversized padding", padded: true, padding: []byte{255}, bad: true},
				{name: "padding includes extension", ext: true, padded: true, padding: []byte{8}, bad: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					header := make([]byte, 12)
					header[0], header[1] = 0x80, payloadH264
					var plain []byte
					if tc.ext {
						header[0] |= 0x10
						header = append(header, 0xbe, 0xde, 0, 1)
						plain = append(plain, 0x10, 0xaa, 0, 0)
					}
					if tc.padded {
						header[0] |= 0x20
					}
					plain = append(plain, want...)
					plain = append(plain, tc.padding...)
					gotHeader, got, err := w.open(m.seal(header, plain))
					if tc.bad {
						if err == nil {
							t.Fatal("accepted invalid RTP padding")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(gotHeader, header) || !bytes.Equal(got, want) {
						t.Fatalf("header %x, payload %x; want header %x, payload %x", gotHeader, got, header, want)
					}
				})
			}
		})
	}
}

// TestWatchReceive sends pictures the way a stream is sent, encrypted and
// cut into packets, and checks that the watcher rebuilds them exactly.
func TestWatchReceive(t *testing.T) {
	var secret [32]byte
	for i := range secret {
		secret[i] = byte(i * 7)
	}
	recv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	send, err := net.DialUDP("udp", nil, recv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	m := &media{conn: send, ssrc: 4242}
	if err := m.useSecret(modes[0], secret); err != nil {
		t.Fatal(err)
	}

	streamer := discord.UserID(77)
	dec := &lockedBuffer{}
	w := &Watch{
		p:         WatchParams{StreamerID: streamer},
		log:       slog.Default(),
		udp:       recv,
		videoSSRC: 4242,
		decIn:     dec,
		done:      make(chan struct{}),
		conn:      &conn{dave: session.New(godave.UserID("1"), nil)},
	}
	block, _ := aes.NewCipher(secret[:])
	w.aead, _ = cipher.NewGCM(block)
	go w.receive()
	defer recv.Close()

	// A small picture in one packet, and a big one cut into FU-A packets.
	small := []byte{0, 0, 0, 1, 0x67, 1, 2, 3, 0, 0, 0, 1, 0x68, 4, 5, 0, 0, 0, 1, 0x65, 9, 9, 9}
	big := []byte{0, 0, 0, 1, 0x65}
	for i := 0; i < 5000; i++ {
		big = append(big, byte(i%251)+1)
	}
	if err := m.sendFrame(small, 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.sendFrame(big, 4000); err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), small...), big...)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(dec.Bytes()) < len(want) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := dec.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("rebuilt %d bytes, want %d; equal prefix %v", len(got), len(want), bytes.HasPrefix(want, got))
	}
}
