//go:build windows && !no_voice

package golive

import (
	"encoding/binary"
	"time"
)

// watchReception holds RFC 3550 reception statistics for one video SSRC.
// The caller holds Watch.mu. H264 timestamps use a 90 kHz clock.
type watchReception struct {
	ssrc                         uint32
	haveSeq                      bool
	base, highest, received      uint32
	priorExpected, priorReceived uint32
	lastArrival                  time.Time
	lastTimestamp                uint32
	jitter                       float64
	lastSR                       uint32
	lastSRAt                     time.Time
}

func (r *watchReception) source(ssrc uint32) {
	if r.ssrc != ssrc {
		*r = watchReception{ssrc: ssrc}
	}
}

func (r *watchReception) record(ssrc uint32, seq uint16, timestamp uint32, now time.Time) {
	r.source(ssrc)
	if !r.haveSeq {
		r.base, r.highest, r.haveSeq = uint32(seq), uint32(seq), true
	} else {
		// A signed modular difference handles sequence wrap and ordinary
		// reordering without counting a late packet as another full cycle.
		delta := int32(int16(seq - uint16(r.highest)))
		if delta > 0 {
			r.highest += uint32(delta)
		}
	}
	r.received++
	if !r.lastArrival.IsZero() {
		arrivalDelta := now.Sub(r.lastArrival).Seconds() * 90000
		timestampDelta := float64(int32(timestamp - r.lastTimestamp))
		d := arrivalDelta - timestampDelta
		if d < 0 {
			d = -d
		}
		r.jitter += (d - r.jitter) / 16
	}
	r.lastArrival, r.lastTimestamp = now, timestamp
}

func (r *watchReception) report(ours, video uint32, now time.Time) []byte {
	r.source(video)
	rr := make([]byte, 8)
	rr[0], rr[1] = 0x80, 201
	binary.BigEndian.PutUint16(rr[2:], 1)
	binary.BigEndian.PutUint32(rr[4:], ours)
	if !r.haveSeq {
		return rr
	}
	rr = append(rr, make([]byte, 24)...)
	rr[0] = 0x81
	binary.BigEndian.PutUint16(rr[2:], 7)
	binary.BigEndian.PutUint32(rr[8:], video)
	expected := r.highest - r.base + 1
	expectedInterval := expected - r.priorExpected
	receivedInterval := r.received - r.priorReceived
	lostInterval := int64(expectedInterval) - int64(receivedInterval)
	if expectedInterval > 0 && lostInterval > 0 {
		rr[12] = byte(min(int64(255), lostInterval*256/int64(expectedInterval)))
	}
	r.priorExpected, r.priorReceived = expected, r.received
	lost := max(int64(-1<<23), min(int64(1<<23-1), int64(expected)-int64(r.received)))
	rr[13], rr[14], rr[15] = byte(lost>>16), byte(lost>>8), byte(lost)
	binary.BigEndian.PutUint32(rr[16:], r.highest)
	binary.BigEndian.PutUint32(rr[20:], uint32(r.jitter))
	binary.BigEndian.PutUint32(rr[24:], r.lastSR)
	if !r.lastSRAt.IsZero() {
		delay := max(0.0, now.Sub(r.lastSRAt).Seconds()) * 65536
		binary.BigEndian.PutUint32(rr[28:], uint32(min(delay, float64(^uint32(0)))))
	}
	return rr
}

// receiveReport opens rtpsize RTCP (eight authenticated header bytes),
// then finds sender reports, including those within a compound packet.
func (w *Watch) receiveReport(pkt []byte, now time.Time) {
	if w.aead == nil || len(pkt) < 8+4+w.aead.Overhead() {
		return
	}
	var nonce [24]byte
	copy(nonce[:4], pkt[len(pkt)-4:])
	body, err := w.aead.Open(nil, nonce[:w.aead.NonceSize()], pkt[8:len(pkt)-4], pkt[:8])
	if err != nil {
		return
	}
	plain := append(append([]byte(nil), pkt[:8]...), body...)
	for len(plain) >= 4 {
		size := (int(binary.BigEndian.Uint16(plain[2:])) + 1) * 4
		if plain[0]>>6 != 2 || size < 4 || size > len(plain) {
			return
		}
		if plain[1] == 200 && size >= 28 {
			ssrc := binary.BigEndian.Uint32(plain[4:])
			w.mu.Lock()
			if ssrc == w.videoSSRC {
				w.reception.source(ssrc)
				// Middle 32 bits of the sender's 64-bit NTP timestamp.
				w.reception.lastSR = binary.BigEndian.Uint32(plain[10:14])
				w.reception.lastSRAt = now
			}
			w.mu.Unlock()
		}
		plain = plain[size:]
	}
}
