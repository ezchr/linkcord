//go:build windows && !no_voice

package golive

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/codecs"
	"github.com/thomas-vilte/dave-go/frame"
	"github.com/thomas-vilte/dave-go/session"
	"github.com/thomas-vilte/mls-go/credentials"
)

// TestWatchReceiveDAVE authenticates pictures after the production receive
// path rebuilds them. The epoch is local; no Discord account is required.
func TestWatchReceiveDAVE(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			dave := session.New(godave.UserID("77"), nil)
			defer dave.Close()
			dave.SetChannelID(987654321)
			dave.OnSelectProtocolAck(1)
			privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			publicKey, err := privateKey.PublicKey.ECDH()
			if err != nil {
				t.Fatal(err)
			}
			// The external sender package is VL(public key) + credential.
			key := publicKey.Bytes()
			pkg := append([]byte{0x40 | byte(len(key)>>8), byte(len(key))}, key...)
			pkg = append(pkg, credentials.NewBasicCredentialFromUint64(1).Marshal()...)
			dave.OnDaveMLSExternalSenderPackage(pkg)
			dave.OnDavePrepareTransition(0, 1)
			if !dave.Ready() {
				t.Fatal("local DAVE epoch is not ready")
			}

			recv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			send, err := net.DialUDP("udp", nil, recv.LocalAddr().(*net.UDPAddr))
			if err != nil {
				recv.Close()
				t.Fatal(err)
			}
			defer send.Close()
			m := &media{conn: send, ssrc: 4242}
			if err := m.useSecret(mode, [32]byte{}); err != nil {
				recv.Close()
				t.Fatal(err)
			}
			dec := &lockedBuffer{}
			w := &Watch{
				p:         WatchParams{StreamerID: discord.UserID(77)},
				log:       slog.Default(),
				udp:       recv,
				aead:      m.aead,
				videoSSRC: m.ssrc,
				decIn:     dec,
				done:      make(chan struct{}),
				conn:      &conn{dave: dave},
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				w.receive()
			}()
			defer func() {
				recv.Close()
				<-finished
			}()

			// AUD, SEI, SPS, PPS, and a slice with mixed Annex B start codes.
			small := []byte{
				0, 0, 1, 0x09, 0xf0,
				0, 0, 0, 1, 0x06, 5, 1, 0x80,
				0, 0, 1, 0x67, 0x42,
				0, 0, 0, 1, 0x68, 0xce,
				0, 0, 1, 0x65, 0x88, 0x80, 0x11, 0x22, 0x33,
			}
			big := append([]byte{0, 0, 0, 1, 0x65, 0x88, 0x80}, bytes.Repeat([]byte{0x55}, 5000)...)
			var want []byte
			for i, pic := range [][]byte{small, big, small} {
				plain, _, err := codecs.H264Frame(pic)
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := dave.EncryptH264(pic)
				if err != nil {
					t.Fatal(err)
				}
				if !frame.LooksLikeDAVEFrame(encrypted) {
					t.Fatal("test picture was not DAVE encrypted")
				}
				want = append(want, plain...)
				ts := uint32(1000 + i*3000)
				if i < 2 {
					if err := m.sendFrame(encrypted, ts); err != nil {
						t.Fatal(err)
					}
					continue
				}
				// Aggregate the last picture in STAP-A and surround it with
				// an encrypted extension body and RTP padding.
				payload := []byte{0x78}
				for _, nal := range splitNALs(encrypted) {
					payload = binary.BigEndian.AppendUint16(payload, uint16(len(nal)))
					payload = append(payload, nal...)
				}
				header := make([]byte, 12)
				header[0], header[1] = 0xb0, payloadH264|0x80
				binary.BigEndian.PutUint16(header[2:], m.seq)
				binary.BigEndian.PutUint32(header[4:], ts)
				binary.BigEndian.PutUint32(header[8:], m.ssrc)
				header = append(header, 0xbe, 0xde, 0, 1)
				body := append([]byte{0x10, 0xaa, 0, 0}, payload...)
				body = append(body, 0, 0, 0, 4)
				if _, err := send.Write(m.seal(header, body)); err != nil {
					t.Fatal(err)
				}
			}

			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && w.assembled.Load() < 3 {
				time.Sleep(10 * time.Millisecond)
			}
			// Join the receive goroutine before inspecting its final output.
			recv.Close()
			<-finished
			if got := dec.Bytes(); !bytes.Equal(got, want) {
				t.Fatalf("decoded %d bytes, want %d; assembled=%d DAVE failures=%d lost=%d",
					len(got), len(want), w.assembled.Load(), w.daveFailed.Load(), w.lost.Load())
			}
			if w.daveFailed.Load() != 0 || w.lost.Load() != 0 {
				t.Fatalf("DAVE failures=%d lost=%d", w.daveFailed.Load(), w.lost.Load())
			}
		})
	}
}
