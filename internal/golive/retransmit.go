package golive

import (
	"encoding/binary"
	"fmt"
	"time"
)

type sentPacket struct {
	valid   bool
	seq     uint16
	ts      uint32
	marker  bool
	payload []byte
	at      time.Time
}

// receiveFeedback authenticates RTCP before honoring requests. Compound RTCP
// is encrypted as one datagram, with its first eight bytes in the clear.
func (m *media) receiveFeedback(pkt []byte) {
	m.mu.Lock()
	if m.aead == nil || len(pkt) < 12+m.aead.Overhead() {
		m.mu.Unlock()
		return
	}
	var nonce [24]byte
	copy(nonce[:4], pkt[len(pkt)-4:])
	body, err := m.aead.Open(nil, nonce[:m.aead.NonceSize()], pkt[8:len(pkt)-4], pkt[:8])
	if err != nil {
		m.mu.Unlock()
		return
	}
	video := m.ssrc
	m.mu.Unlock()
	plain := append(append([]byte(nil), pkt[:8]...), body...)
	for len(plain) >= 4 {
		size := (int(binary.BigEndian.Uint16(plain[2:])) + 1) * 4
		if plain[0]>>6 != 2 || size < 4 || size > len(plain) {
			return
		}
		p := plain[:size]
		typ, format := p[1], p[0]&0x1f
		m.rtcpMu.Lock()
		if m.rtcp == nil {
			m.rtcp = make(map[string]int)
		}
		m.rtcp[fmt.Sprintf("%d/%d", typ, format)]++
		m.rtcpMu.Unlock()
		if typ == 205 && format == 1 && len(p) >= 16 && binary.BigEndian.Uint32(p[8:]) == video {
			for _, seq := range nackSequences(p[12:]) {
				m.retransmit(seq)
			}
		}
		if typ == 206 && len(p) >= 12 {
			target := binary.BigEndian.Uint32(p[8:])
			if (format == 1 && target == video) ||
				(format == 4 && len(p) >= 20 && binary.BigEndian.Uint32(p[12:]) == video) {
				select {
				case m.keyframe <- struct{}{}:
				default:
				}
			}
		}
		plain = plain[size:]
	}
}

func (m *media) retransmit(seq uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.history[int(seq)%len(m.history)]
	if !p.valid || p.seq != seq || time.Since(p.at) > 2*time.Second || m.aead == nil || m.rtxSSRC == 0 {
		m.resendMissed++
		return nil
	}
	m.resent++
	header := make([]byte, 12)
	header[0], header[1] = 0x80, payloadH264RTX
	if p.marker {
		header[1] |= 0x80
	}
	binary.BigEndian.PutUint16(header[2:], m.rtxSeq)
	m.rtxSeq++
	binary.BigEndian.PutUint32(header[4:], p.ts)
	binary.BigEndian.PutUint32(header[8:], m.rtxSSRC)
	payload := binary.BigEndian.AppendUint16(nil, p.seq)
	payload = append(payload, p.payload...)
	_, err := m.conn.Write(m.seal(header, payload))
	return err
}
