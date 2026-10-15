package golive

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/thomas-vilte/dave-go/codecs"
	"github.com/thomas-vilte/dave-go/frame"
)

// TestRoundTrip sends a DAVE-encrypted H264 frame as the stream does, and
// receives it as a viewer would: it opens the transport's encryption, joins
// the FU-A fragments, rebuilds the frame with 4-byte start codes, as
// WebRTC's depacketizer does, and decrypts it.
func TestRoundTrip(t *testing.T) {
	big := make([]byte, 3500)
	rand.Read(big)
	for i := range big {
		if big[i] == 0 {
			big[i] = 1
		}
	}
	idr := append([]byte{0x65, 0x88, 0x80}, big...)
	plain := bytes.Join([][]byte{nil, {0x67, 0x42, 0xc0, 0x1f}, {0x68, 0xce, 0x3c, 0x80}, idr, {0x41, 0x9a, 0x01, 0x02}}, []byte{0, 0, 1})
	normal, ranges, err := codecs.H264Frame(plain)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 16)
	rand.Read(key)
	encrypted, err := frame.Encrypt(frame.EncryptParams{Plaintext: normal, Key: key, TruncatedNonce: 5, UnencryptedRanges: ranges})
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	conn, err := net.Dial("udp", l.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	m := &media{conn: conn, ssrc: 1234, closed: make(chan struct{})}
	var secret [32]byte
	rand.Read(secret[:])
	if err := m.useSecret(modes[0], secret); err != nil {
		t.Fatal(err)
	}
	if err := m.sendFrame(encrypted, 9000); err != nil {
		t.Fatal(err)
	}

	recv := &media{}
	recv.useSecret(modes[0], secret)
	var rebuilt []byte
	var fu []byte
	marker := false
	var transportSeq uint16
	buf := make([]byte, 2048)
	l.SetReadDeadline(time.Now().Add(2 * time.Second))
	for !marker {
		n, err := l.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		pkt := buf[:n]
		// Extended as Discord's clients send video: the extension's header
		// readable, its elements encrypted.
		if pkt[0] != 0x90 || pkt[1]&0x7F != payloadH264 || binary.BigEndian.Uint32(pkt[8:]) != 1234 || binary.BigEndian.Uint32(pkt[4:]) != 9000 {
			t.Fatalf("bad RTP header % x", pkt[:12])
		}
		if !bytes.Equal(pkt[12:16], []byte{0xbe, 0xde, 0, 2}) {
			t.Fatalf("bad extension header % x", pkt[12:16])
		}
		marker = pkt[1]&0x80 != 0
		var nonce [12]byte
		copy(nonce[:4], pkt[n-4:])
		payload, err := recv.aead.Open(nil, nonce[:], pkt[16:n-4], pkt[:16])
		if err != nil {
			t.Fatal("transport decryption:", err)
		}
		if payload[0] != extAbsSendTime<<4|2 || payload[4] != extTransportSeq<<4|1 || payload[7] != 0 {
			t.Fatalf("bad extension elements % x", payload[:8])
		}
		if seq := binary.BigEndian.Uint16(payload[5:]); seq != transportSeq {
			t.Fatalf("transport-wide sequence %d, want %d", seq, transportSeq)
		}
		transportSeq++
		payload = payload[8:]
		if payload[0]&0x1F == 28 {
			if payload[1]&0x80 != 0 {
				fu = []byte{payload[0]&0xE0 | payload[1]&0x1F}
			}
			fu = append(fu, payload[2:]...)
			if payload[1]&0x40 != 0 {
				rebuilt = append(append(rebuilt, 0, 0, 0, 1), fu...)
			}
			continue
		}
		rebuilt = append(append(rebuilt, 0, 0, 0, 1), payload...)
	}
	if !bytes.Equal(rebuilt, encrypted) {
		t.Fatalf("the frame rebuilt (%d bytes) is not the one sent (%d)", len(rebuilt), len(encrypted))
	}
	decrypted, _, err := frame.Decrypt(frame.DecryptParams{Ciphertext: rebuilt, Key: key})
	if err != nil {
		t.Fatal("DAVE decryption:", err)
	}
	if !bytes.Equal(decrypted, normal) {
		t.Fatal("the frame decrypted is not the one encrypted")
	}
}

// TestSessionDescription reads Op 4 as Discord sends it, with its media
// session ID as a string; failing to read it once kept DAVE from starting.
func TestSessionDescription(t *testing.T) {
	op, ok := unmarshalers.Lookup(4, "")().(*sessionDescriptionEvent)
	if !ok {
		t.Fatal("Op 4 is not the stream's session description")
	}
	raw := `{"video_codec":"H264","secure_frames_version":0,"secret_key":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32],"mode":"aead_aes256_gcm_rtpsize","media_session_id":"a1b2c3","audio_codec":"opus","dave_protocol_version":1}`
	if err := json.Unmarshal([]byte(raw), op); err != nil {
		t.Fatal(err)
	}
	if op.VideoCodec != "H264" || op.DAVEProtocolVersion != 1 || op.SecretKey[31] != 32 || op.Mode != "aead_aes256_gcm_rtpsize" {
		t.Fatalf("read %+v", op)
	}
}
