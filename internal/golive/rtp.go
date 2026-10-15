package golive

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// The encryption modes of the transport, the preferred first.
var modes = []string{"aead_aes256_gcm_rtpsize", "aead_xchacha20_poly1305_rtpsize"}

// media is the UDP connection to a stream server, which video is sent on.
type media struct {
	conn net.Conn
	aead cipher.AEAD

	mu sync.Mutex
	// nonce is the counter in the first 4 bytes of the nonce of the
	// transport's encryption, sent after every packet.
	nonce [24]byte
	// The RTP state of the sound.
	audioSSRC uint32
	audioSeq  uint16
	// The RTP state of the video.
	ssrc     uint32
	seq      uint16
	rtxSSRC  uint32
	// resent and resendMissed count the packets resent for NACKs, and
	// those asked for that could not be.
	resent, resendMissed int
	rtxSeq   uint16
	history  [1024]sentPacket
	packets  uint32
	octets   uint32
	lastTS   uint32
	lastSent time.Time
	buf      []byte
	// linkcordOnly sends the playout delay extension in place of what
	// Discord's clients expect, for a stream that only Linkcord shows.
	linkcordOnly bool
	// transportSeq numbers the video and RTX packets for the transport-wide
	// congestion control extension.
	transportSeq uint16

	// keyframe is sent to when a receiver asks for a keyframe.
	keyframe chan struct{}
	// rtcp counts the RTCP packets received, by type and format.
	rtcpMu sync.Mutex
	rtcp   map[string]int
	closed chan struct{}
	once   sync.Once
}

// dialMedia connects to the stream server at addr, discovers the address it
// sees for the client, as voice connections do, and returns it.
func dialMedia(ctx context.Context, addr string, ssrc uint32) (*media, string, uint16, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, "", 0, fmt.Errorf("golive: could not reach the stream server: %w", err)
	}
	if uc, ok := conn.(*net.UDPConn); ok {
		// Keyframes go out as a burst of packets.
		uc.SetWriteBuffer(4 << 20)
	}
	var discovery [74]byte
	binary.BigEndian.PutUint16(discovery[0:], 1)
	binary.BigEndian.PutUint16(discovery[2:], 70)
	binary.BigEndian.PutUint32(discovery[4:], ssrc)
	if _, err := conn.Write(discovery[:]); err != nil {
		conn.Close()
		return nil, "", 0, err
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var reply [74]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		conn.Close()
		return nil, "", 0, fmt.Errorf("golive: no answer to the address discovery: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	body := reply[8:72]
	end := bytes.IndexByte(body, 0)
	if end < 0 {
		conn.Close()
		return nil, "", 0, errors.New("golive: bad address discovery answer")
	}
	m := &media{conn: conn, keyframe: make(chan struct{}, 1), closed: make(chan struct{})}
	go m.read()
	go m.keepAlive()
	return m, string(body[:end]), binary.BigEndian.Uint16(reply[72:]), nil
}

// useSecret encrypts with secret in mode.
func (m *media) useSecret(mode string, secret [32]byte) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch mode {
	case modes[0]:
		var block cipher.Block
		if block, err = aes.NewCipher(secret[:]); err == nil {
			m.aead, err = cipher.NewGCM(block)
		}
	case modes[1]:
		m.aead, err = chacha20poly1305.NewX(secret[:])
	default:
		err = fmt.Errorf("golive: unknown encryption mode %q", mode)
	}
	return err
}

func (m *media) close() {
	m.once.Do(func() {
		close(m.closed)
		m.conn.Close()
	})
}

// keepAlive pings the server every 5 seconds, so that what is between them
// keeps letting packets through.
func (m *media) keepAlive() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	ping := []byte{0x13, 0x37, 0xCA, 0xFE, 0, 0, 0, 0}
	for seq := uint32(0); ; seq++ {
		binary.BigEndian.PutUint32(ping[4:], seq)
		if _, err := m.conn.Write(ping); err != nil {
			return
		}
		select {
		case <-t.C:
		case <-m.closed:
			return
		}
	}
}

// read watches for receivers asking for a keyframe: RTCP payload-specific
// feedback, a picture loss indication or a full intra request. Their headers
// are not encrypted.
func (m *media) read() {
	buf := make([]byte, 2048)
	for {
		n, err := m.conn.Read(buf)
		if err != nil {
			return
		}
		if n >= 8 && buf[0]>>6 == 2 && buf[1] >= 192 && buf[1] <= 223 {
			m.receiveFeedback(buf[:n])
		}
	}
}

// rtcpSummary tells the RTCP packets received, as type/format=count.
func (m *media) rtcpSummary() string {
	m.rtcpMu.Lock()
	defer m.rtcpMu.Unlock()
	var parts []string
	for k, n := range m.rtcp {
		parts = append(parts, fmt.Sprintf("%s=%d", k, n))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

func (m *media) packetCount() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.packets
}

// seal encrypts plaintext after header, which it authenticates, and appends
// the nonce's counter, as the rtpsize modes do.
func (m *media) seal(header, plaintext []byte) []byte {
	binary.BigEndian.PutUint32(m.nonce[:4], binary.BigEndian.Uint32(m.nonce[:4])+1)
	m.buf = append(m.buf[:0], header...)
	m.buf = m.aead.Seal(m.buf, m.nonce[:m.aead.NonceSize()], plaintext, header)
	return append(m.buf, m.nonce[:4]...)
}

// mtu is the most of a NAL unit sent in a packet.
const mtu = 1100

// sendFrame sends an H264 access unit, in Annex B with 4-byte start codes,
// at the RTP timestamp ts, as Single NAL Unit and FU-A packets.
func (m *media) sendFrame(frame []byte, ts uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	nals := splitNALs(frame)
	header := make([]byte, 12, 12+4)
	payload := make([]byte, 0, mtu+8)
	sent := 0
	send := func(last bool, p []byte) error {
		header = header[:12]
		header[0] = 0x80 // version 2
		header[1] = payloadH264
		if last {
			header[1] |= 0x80
		}
		binary.BigEndian.PutUint16(header[2:], m.seq)
		m.seq++
		binary.BigEndian.PutUint32(header[4:], ts)
		binary.BigEndian.PutUint32(header[8:], m.ssrc)
		pkt := m.seal(m.withExtension(header), m.extended(p))
		if _, err := m.conn.Write(pkt); err != nil {
			return err
		}
		seq := binary.BigEndian.Uint16(header[2:])
		m.history[int(seq)%len(m.history)] = sentPacket{
			valid: true, seq: seq, ts: ts, marker: last,
			payload: bytes.Clone(p), at: time.Now(),
		}
		m.packets++
		m.octets += uint32(len(p))
		// A breath every few packets, so that a keyframe does not flood the
		// buffers on the way.
		if sent++; sent%16 == 0 {
			time.Sleep(time.Millisecond)
		}
		return nil
	}
	for i, nal := range nals {
		lastNAL := i == len(nals)-1
		if len(nal) <= mtu {
			payload = append(payload[:0], nal...)
			if err := send(lastNAL, payload); err != nil {
				return err
			}
			continue
		}
		// FU-A: the indicator carries the NAL's F and NRI bits, the header
		// its type, and the start and end of the unit.
		indicator := nal[0]&0xE0 | 28
		typ := nal[0] & 0x1F
		rest := nal[1:]
		for first := true; len(rest) > 0; first = false {
			n := min(len(rest), mtu-2)
			fu := typ
			if first {
				fu |= 0x80
			}
			if n == len(rest) {
				fu |= 0x40
			}
			payload = append(payload[:0], indicator, fu)
			payload = append(payload, rest[:n]...)
			rest = rest[n:]
			if err := send(lastNAL && len(rest) == 0, payload); err != nil {
				return err
			}
		}
	}
	m.lastTS, m.lastSent = ts, time.Now()
	return nil
}

// payloadOpusRTP is the payload type of the sound.
const payloadOpusRTP = payloadOpus

// sendAudio sends an Opus frame, encrypted end to end, at the RTP
// timestamp ts, marked when it starts sound after silence.
func (m *media) sendAudio(frame []byte, ts uint32, marker bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = payloadOpusRTP
	if marker {
		header[1] |= 0x80
	}
	binary.BigEndian.PutUint16(header[2:], m.audioSeq)
	m.audioSeq++
	binary.BigEndian.PutUint32(header[4:], ts)
	binary.BigEndian.PutUint32(header[8:], m.audioSSRC)
	_, err := m.conn.Write(m.seal(header, frame))
	return err
}

// ntpEpoch is when NTP time starts.
var ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// sendReport sends an RTCP sender report, which receivers use to keep the
// video in time.
func (m *media) sendReport() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastSent.IsZero() {
		return nil
	}
	header := make([]byte, 8)
	header[0], header[1] = 0x80, 200
	binary.BigEndian.PutUint16(header[2:], 6)
	binary.BigEndian.PutUint32(header[4:], m.ssrc)
	report := make([]byte, 20)
	since := m.lastSent.Sub(ntpEpoch)
	secs := uint64(since / time.Second)
	frac := uint64(since%time.Second) << 32 / uint64(time.Second)
	binary.BigEndian.PutUint32(report[0:], uint32(secs))
	binary.BigEndian.PutUint32(report[4:], uint32(frac))
	binary.BigEndian.PutUint32(report[8:], m.lastTS)
	binary.BigEndian.PutUint32(report[12:], m.packets)
	binary.BigEndian.PutUint32(report[16:], m.octets)
	_, err := m.conn.Write(m.seal(header, report))
	return err
}

// splitNALs returns the NAL units of an Annex B access unit, without their
// start codes.
func splitNALs(frame []byte) [][]byte {
	var nals [][]byte
	start := -1
	for i := 0; i+2 < len(frame); {
		if frame[i] == 0 && frame[i+1] == 0 && frame[i+2] == 1 {
			if start >= 0 {
				end := i
				// The zero before a 4-byte start code is not part of the unit.
				if end > start && frame[end-1] == 0 {
					end--
				}
				nals = append(nals, frame[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start < len(frame) {
		nals = append(nals, frame[start:])
	}
	return nals
}

// The video's header extension carries what Discord's own clients send, and
// Discord's servers expect: abs-send-time (ID 3, three bytes) and the
// transport-wide sequence number (ID 5, two bytes), counted over every
// video and RTX packet, then a byte of padding to fill the second word.
const (
	extAbsSendTime   = 3
	extTransportSeq  = 5
	extensionElement = 8
)

// withExtension marks the 12-byte video header as extended and adds the
// extension's own header (the one-byte profile, two words long), which stay
// readable; extended puts the extension's elements before the payload, which
// is encrypted with them.
func (m *media) withExtension(header []byte) []byte {
	header[0] |= 0x10
	if m.linkcordOnly {
		return append(header, 0xBE, 0xDE, 0, uint8(len(playoutDelay)/4))
	}
	return append(header, 0xBE, 0xDE, 0, extensionElement/4)
}

// playoutDelay is the extension of a stream for Linkcord only: the playout
// delay (ID 5, three bytes) at zero. Discord's servers read ID 5 as the
// transport-wide sequence number, which three bytes are not, and Discord's
// clients do not show the stream; Linkcord does.
var playoutDelay = []byte{5<<4 | 2, 0, 0, 0}

func (m *media) extended(p []byte) []byte {
	if m.linkcordOnly {
		out := make([]byte, 0, len(playoutDelay)+len(p))
		return append(append(out, playoutDelay...), p...)
	}
	// abs-send-time is seconds in 6.18 fixed point, its low 24 bits.
	now := uint64(time.Now().UnixNano())
	abs := uint32((now << 18) / uint64(time.Second))
	seq := m.transportSeq
	m.transportSeq++
	out := make([]byte, 0, extensionElement+len(p))
	out = append(out, extAbsSendTime<<4|2, byte(abs>>16), byte(abs>>8), byte(abs))
	out = append(out, extTransportSeq<<4|1, byte(seq>>8), byte(seq), 0)
	return append(out, p...)
}
