//go:build windows && !no_voice

package golive

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
)

func TestWatchNACKRTXRecovery(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer relay.Close()
			send, err := net.DialUDP("udp", nil, relay.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer send.Close()
			recv, err := net.DialUDP("udp", nil, relay.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			m := &media{conn: send, ssrc: 4242, rtxSSRC: 4243, keyframe: make(chan struct{}, 1)}
			if err := m.useSecret(mode, [32]byte{}); err != nil {
				t.Fatal(err)
			}
			dave := session.New(godave.UserID("1"), nil)
			defer dave.Close()
			dec := &lockedBuffer{}
			w := &Watch{
				p:   WatchParams{StreamerID: discord.UserID(77)},
				log: slog.Default(), udp: recv, aead: m.aead,
				videoSSRC: m.ssrc, rtxSSRC: m.rtxSSRC, ourSSRC: 99,
				decIn: dec, done: make(chan struct{}), conn: &conn{dave: dave},
			}
			finished := make(chan struct{})
			go func() { defer close(finished); w.receive() }()
			defer func() { recv.Close(); <-finished }()

			frame := append([]byte{0, 0, 0, 1, 0x65}, bytes.Repeat([]byte{0x55}, 5000)...)
			if err := m.sendFrame(frame, 9000); err != nil {
				t.Fatal(err)
			}
			relay.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 2048)
			var packets [][]byte
			for {
				n, _, err := relay.ReadFromUDP(buf)
				if err != nil {
					t.Fatal(err)
				}
				packets = append(packets, bytes.Clone(buf[:n]))
				if buf[1]&0x80 != 0 {
					break
				}
			}
			if len(packets) < 4 {
				t.Fatal("test frame was not fragmented")
			}
			// Deliver a start, later fragments out of order, and a duplicate.
			for _, index := range []int{0, 3, 2, 2} {
				if _, err := relay.WriteToUDP(packets[index], recv.LocalAddr().(*net.UDPAddr)); err != nil {
					t.Fatal(err)
				}
			}
			for index := 4; index < len(packets); index++ {
				relay.WriteToUDP(packets[index], recv.LocalAddr().(*net.UDPAddr))
			}
			n, _, err := relay.ReadFromUDP(buf)
			if err != nil {
				t.Fatal("no NACK:", err)
			}
			feedback := bytes.Clone(buf[:n])
			if feedback[1] != 205 {
				t.Fatalf("expected NACK, got %x", feedback[:8])
			}
			// An altered feedback packet must not trigger retransmission.
			bad := bytes.Clone(feedback)
			bad[8] ^= 1
			before := m.rtxSeq
			m.receiveFeedback(bad)
			if m.rtxSeq != before {
				t.Fatal("unauthenticated feedback triggered retransmission")
			}
			m.receiveFeedback(feedback)
			for len(dec.Bytes()) < len(frame) {
				relay.SetReadDeadline(time.Now().Add(2 * time.Second))
				n, _, err := relay.ReadFromUDP(buf)
				if err != nil {
					t.Fatal("no RTX:", err)
				}
				if buf[1]&0x7f == payloadH264RTX {
					if binary.BigEndian.Uint32(buf[8:]) != m.rtxSSRC {
						t.Fatal("wrong RTX SSRC")
					}
					relay.WriteToUDP(buf[:n], recv.LocalAddr().(*net.UDPAddr))
				} else if buf[1] == 205 {
					m.receiveFeedback(buf[:n])
				}
				deadline := time.Now().Add(50 * time.Millisecond)
				for time.Now().Before(deadline) && len(dec.Bytes()) < len(frame) {
					time.Sleep(time.Millisecond)
				}
			}
			if !bytes.Equal(dec.Bytes(), frame) {
				t.Fatal("recovered picture differs from original")
			}
			if w.lost.Load() != 0 {
				t.Fatalf("repair still counted as picture loss: %d", w.lost.Load())
			}
		})
	}
}
