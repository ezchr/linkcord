package golive

import (
	"bytes"
	"crypto/rand"
	"net"
	"testing"
	"time"
)

// TestLinkcordOnlyNoExtension checks that a stream for Linkcord only goes
// out without a header extension, as Linkcord's viewers have always read.
func TestLinkcordOnlyNoExtension(t *testing.T) {
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
	if buf[0] != 0x80 {
		t.Fatalf("bad header % x", buf[:12])
	}
	w := &Watch{}
	w.useSecret(modes[0], secret)
	_, payload, err := w.open(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, nal) {
		t.Fatalf("payload % x, want % x", payload, nal)
	}
}
