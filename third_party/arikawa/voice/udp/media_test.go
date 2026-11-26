package udp

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// seal builds an rtpsize packet as a voice server sends it: the header (and
// the first 4 bytes of an extension) in the clear, the rest sealed, the
// nonce's counter last.
func seal(t *testing.T, aead cipher.AEAD, pt byte, ssrc uint32, seq uint16, ext, payload []byte, n uint32) []byte {
	t.Helper()
	h := make([]byte, 12)
	h[0], h[1] = 0x80, pt
	binary.BigEndian.PutUint16(h[2:], seq)
	binary.BigEndian.PutUint32(h[8:], ssrc)
	plain := payload
	if ext != nil {
		h[0] |= 0x10
		h = append(h, 0xBE, 0xDE, 0, byte(len(ext)/4))
		plain = append(append([]byte(nil), ext...), payload...)
	}
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[:4], n)
	out := aead.Seal(append([]byte(nil), h...), nonce[:], plain, h)
	return append(out, nonce[:4]...)
}

func TestMediaHandler(t *testing.T) {
	a, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip(err)
	}
	defer a.Close()
	b, err := net.DialUDP("udp", nil, a.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var key [32]byte
	block, _ := aes.NewCipher(key[:])
	aead, _ := cipher.NewGCM(block)
	c := &Connection{conn: a, recvBuf: make([]byte, recvSize), recvOpus: make([]byte, recvSize), recvPacket: &Packet{}, aead: aead}

	var got []MediaPacket
	c.SetMediaHandler(func(p *MediaPacket) bool {
		got = append(got, MediaPacket{
			Header:    append([]byte(nil), p.Header...),
			Extension: append([]byte(nil), p.Extension...),
			Payload:   append([]byte(nil), p.Payload...),
		})
		return p.PayloadType() != 0x78
	})

	big := make([]byte, 1300)
	for i := range big {
		big[i] = byte(i)
	}
	ext := []byte{0x12, 0x00, 0x05, 0x00}
	b.Write(seal(t, aead, 103|0x80, 7, 9, ext, big, 1))
	b.Write([]byte{0x80, 201, 0, 1, 1, 2, 3, 4}) // RTCP: skipped
	b.Write([]byte("garbage"))
	b.Write(seal(t, aead, 0x78, 5, 1, nil, []byte("opus"), 2))

	a.SetReadDeadline(time.Now().Add(5 * time.Second))
	p, err := c.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Opus) != "opus" || p.SSRC() != 5 {
		t.Fatalf("opus packet: %q ssrc %d", p.Opus, p.SSRC())
	}
	if len(got) != 2 {
		t.Fatalf("handler saw %d packets, want 2", len(got))
	}
	v := got[0]
	if v.PayloadType() != 103 || !v.Marker() || v.SSRC() != 7 || v.Sequence() != 9 {
		t.Fatalf("video header: pt %d marker %v ssrc %d seq %d", v.PayloadType(), v.Marker(), v.SSRC(), v.Sequence())
	}
	if v.ExtensionProfile() != 0xBEDE || string(v.Extension) != string(ext) || string(v.Payload) != string(big) {
		t.Fatalf("video body: profile %x ext %x payload %d bytes", v.ExtensionProfile(), v.Extension, len(v.Payload))
	}
}

func TestWriteRTCPSharesNonce(t *testing.T) {
	a, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip(err)
	}
	defer a.Close()
	b, err := net.DialUDP("udp", nil, a.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var key [32]byte
	block, _ := aes.NewCipher(key[:])
	aead, _ := cipher.NewGCM(block)
	c := &Connection{conn: b, aead: aead}
	if err := c.WriteRTCP([]byte{0x81, 206, 0, 2, 0, 0, 0, 1, 0, 0, 0, 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRTCP([]byte{1, 2}); err == nil {
		t.Fatal("a short packet was sent")
	}
	buf := make([]byte, 100)
	a.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := a.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	pkt := buf[:n]
	var nonce [12]byte
	copy(nonce[:4], pkt[n-4:])
	plain, err := aead.Open(nil, nonce[:], pkt[8:n-4], pkt[:8])
	if err != nil || binary.BigEndian.Uint32(plain) != 2 {
		t.Fatalf("RTCP did not open: %v", err)
	}
	if c.nonceCount.Load() != 1 {
		t.Fatalf("nonce counter %d", c.nonceCount.Load())
	}
}
