//go:build windows && !no_voice

package golive

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
	"github.com/thomas-vilte/mls-go/credentials"
	"github.com/tphakala/go-opus/opus"
)

// TestWatchAudioDAVE plays stream sound encrypted end to end, as Discord
// sends it with DAVE on, through the same path as the clear test.
func TestWatchAudioDAVE(t *testing.T) {
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
	key := publicKey.Bytes()
	pkg := append([]byte{0x40 | byte(len(key)>>8), byte(len(key))}, key...)
	pkg = append(pkg, credentials.NewBasicCredentialFromUint64(1).Marshal()...)
	dave.OnDaveMLSExternalSenderPackage(pkg)
	dave.OnDavePrepareTransition(0, 1)
	if !dave.Ready() {
		t.Fatal("local DAVE epoch is not ready")
	}
	const audioSSRC = 123
	dave.AssignSsrcToCodec(audioSSRC, godave.CodecOpus)

	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960*2)
	var packets [][]byte
	for frame := 0; frame < 3; frame++ {
		for i := 0; i < 960; i++ {
			v := int16(8000 * math.Sin(2*math.Pi*440*float64(frame*960+i)/48000))
			pcm[2*i], pcm[2*i+1] = v, v
		}
		buf := make([]byte, 1276)
		n, err := encoder.Encode(pcm, buf)
		if err != nil {
			t.Fatal(err)
		}
		sealed := make([]byte, n+64)
		m, err := dave.Encrypt(audioSSRC, buf[:n], sealed)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(sealed[:m], buf[:n]) {
			t.Fatal("the sound was not encrypted")
		}
		packets = append(packets, bytes.Clone(sealed[:m]))
	}

	w := &Watch{
		p:   WatchParams{StreamerID: discord.UserID(77)},
		log: slog.Default(), audioSSRC: audioSSRC, done: make(chan struct{}),
		conn: &conn{dave: dave},
	}
	a := &streamAudio{w: w, input: make(chan audioPacket, 32), done: make(chan struct{})}
	w.audio = a
	finished := make(chan struct{})
	go func() { defer close(finished); a.run() }()
	defer func() { a.stop(); <-finished }()
	for seq, p := range packets {
		w.receiveAudio(audioSSRC, uint16(seq), p)
	}
	deadline := time.Now().Add(time.Second)
	size := 0
	for time.Now().Before(deadline) && size < 3*streamAudioFrame {
		a.mu.Lock()
		size = len(a.pcm)
		a.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	if size != 3*streamAudioFrame {
		t.Fatalf("decrypted and decoded %d bytes, want %d", size, 3*streamAudioFrame)
	}
	out := make([]byte, 3*streamAudioFrame)
	a.fill(out)
	if bytes.Equal(out, make([]byte, len(out))) {
		t.Fatal("the decrypted sound was silent")
	}
}
