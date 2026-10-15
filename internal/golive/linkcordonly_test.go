package golive

import (
	"bytes"
	"crypto/rand"
	"net"
	"testing"
	"time"
)

// TestLinkcordOnlyExtension checks that a stream for Linkcord only carries
// the playout delay extension, and that Linkcord's viewer reads past it to
// the picture.
func TestLinkcordOnlyExtension(t *testing.T) {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	conn, err := net.Dial("udp", l.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	m := &media{conn: conn, ssrc: 1234, closed: make(chan struct{}), linkcordOnly: true}
	var secret [32]byte
	rand.Read(secret[:])
	if err := m.useSecret(modes[0], secret); err != nil {
		t.Fatal(err)
	}
	nal := []byte{0x65, 0x88, 0x80, 1, 2, 3}
	if err := m.sendFrame(append([]byte{0, 0, 0, 1}, nal...), 9000); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	l.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := l.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	pkt := buf[:n]
	if pkt[0] != 0x90 || !bytes.Equal(pkt[12:16], []byte{0xbe, 0xde, 0, 1}) {
		t.Fatalf("bad header % x", pkt[:16])
	}
	w := &Watch{}
	w.useSecret(modes[0], secret)
	_, payload, err := w.open(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, nal) {
		t.Fatalf("payload % x, want % x", payload, nal)
	}
}
