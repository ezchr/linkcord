//go:build windows && !no_voice

package golive

import (
	"bytes"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
	"github.com/tphakala/go-opus/opus"
)

func TestWatchAudioReorderingAndPlayback(t *testing.T) {
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960*2)
	var packets [][]byte
	for frame := 0; frame < 3; frame++ {
		for i := 0; i < 960; i++ {
			sample := int16(8000 * math.Sin(2*math.Pi*440*float64(frame*960+i)/48000))
			pcm[2*i], pcm[2*i+1] = sample, sample
		}
		buf := make([]byte, 1276)
		n, err := encoder.Encode(pcm, buf)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, bytes.Clone(buf[:n]))
	}
	dave := session.New(godave.UserID("1"), nil)
	defer dave.Close()
	w := &Watch{
		p:   WatchParams{StreamerID: discord.UserID(77)},
		log: slog.Default(), audioSSRC: 123, done: make(chan struct{}),
		conn: &conn{dave: dave},
	}
	a := &streamAudio{w: w, input: make(chan audioPacket, 32), done: make(chan struct{})}
	w.audio = a
	finished := make(chan struct{})
	go func() { defer close(finished); a.run() }()
	defer func() { a.stop(); <-finished }()
	w.receiveAudio(999, 0, packets[0])
	if len(a.input) != 0 {
		t.Fatal("accepted unrelated audio SSRC")
	}
	for _, seq := range []uint16{0, 2, 2, 1} {
		w.receiveAudio(123, seq, packets[seq])
	}
	deadline := time.Now().Add(time.Second)
	size := 0
	for time.Now().Before(deadline) {
		a.mu.Lock()
		size = len(a.pcm)
		a.mu.Unlock()
		if size >= 3*streamAudioFrame {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if size != 3*streamAudioFrame {
		t.Fatalf("buffered %d bytes, want %d", size, 3*streamAudioFrame)
	}
	output := make([]byte, 3*streamAudioFrame)
	a.fill(output)
	if bytes.Equal(output, make([]byte, len(output))) {
		t.Fatal("decoded tone was silent")
	}
	a.fill(output)
	if !bytes.Equal(output, make([]byte, len(output))) {
		t.Fatal("audio underrun did not produce silence")
	}
}

func TestWatchAudioBufferBound(t *testing.T) {
	a := &streamAudio{}
	a.appendPCM(make([]int16, 48000*2))
	if len(a.pcm) != 3*streamAudioFrame {
		t.Fatalf("audio latency was not bounded: %d bytes", len(a.pcm))
	}
}
