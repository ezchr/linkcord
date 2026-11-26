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
		if typ == 200 && len(p) >= 28 {
			m.reportBlocks(p[28:], int(format), video)
		}
		if typ == 201 && len(p) >= 8 {
			m.reportBlocks(p[8:], int(format), video)
		}
		if typ == 206 && format == 15 && len(p) >= 20 && string(p[12:16]) == "REMB" {
			exp := p[17] >> 2
			mantissa := int64(p[17]&3)<<16 | int64(p[18])<<8 | int64(p[19])
			m.remb.Store(mantissa << exp)
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
	_, err := m.conn.Write(m.seal(m.withExtension(header), m.extended(payload)))
	return err
}

// reportBlocks reads the loss viewers report of the video, from count
// RTCP report blocks, and moves the share of the bitrate sent as WebRTC's
// loss-based control does: down by half the loss above 10%, up 5% below 2%.
func (m *media) reportBlocks(b []byte, count int, video uint32) {
	for i := 0; i < count && len(b) >= 24; i, b = i+1, b[24:] {
		if binary.BigEndian.Uint32(b) != video {
			continue
		}
		lost := float64(b[4]) / 256
		rate := m.rate.Load()
		if rate == 0 {
			rate = 1000
		}
		switch {
		case lost > 0.10:
			rate = int64(float64(rate) * (1 - 0.5*lost))
		case lost < 0.02:
			rate = rate * 105 / 100
		}
		m.rate.Store(min(max(rate, minRate), 1000))
	}
}

// minRate is the least share of the bitrate sent, in thousandths.
const minRate = 150

// targetBitrate is what to encode at, of the full bitrate.
func (m *media) targetBitrate(full int) int {
	rate := m.rate.Load()
	if rate == 0 {
		rate = 1000
	}
	b := int(int64(full) * rate / 1000)
	if remb := m.remb.Load(); remb > 0 {
		b = min(b, int(remb))
	}
	return max(b, 300_000)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
