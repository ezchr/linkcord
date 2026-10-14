//go:build windows && !no_voice

package golive

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestWatchReceptionReport(t *testing.T) {
	now := time.Unix(100, 0)
	var r watchReception
	if rr := r.report(42, 155, now); len(rr) != 8 || rr[0] != 0x80 || binary.BigEndian.Uint16(rr[2:]) != 1 {
		t.Fatalf("report before media = %x", rr)
	}
	// Wrap the sequence number, losing packet 65535.
	r.record(155, 65534, 0, now)
	r.record(155, 0, 900, now.Add(10*time.Millisecond))
	r.record(155, 1, 1800, now.Add(20*time.Millisecond))
	rr := r.report(42, 155, now.Add(time.Second))
	if rr[0] != 0x81 || rr[1] != 201 || binary.BigEndian.Uint16(rr[2:]) != 7 {
		t.Fatalf("bad receiver report header: %x", rr)
	}
	if binary.BigEndian.Uint32(rr[4:]) != 42 || binary.BigEndian.Uint32(rr[8:]) != 155 {
		t.Fatalf("wrong report SSRCs: %x", rr)
	}
	if rr[12] != 64 || !bytes.Equal(rr[13:16], []byte{0, 0, 1}) {
		t.Fatalf("loss = %x, want fraction 64, cumulative 1", rr[12:16])
	}
	if got := binary.BigEndian.Uint32(rr[16:]); got != 65537 {
		t.Fatalf("extended highest sequence = %d, want 65537", got)
	}
	if got := binary.BigEndian.Uint32(rr[20:]); got != 0 {
		t.Fatalf("constant-transit jitter = %d, want 0", got)
	}
	// A late repair closes the loss without advancing the highest sequence.
	r.record(155, 65535, 900, now.Add(30*time.Millisecond))
	rr = r.report(42, 155, now.Add(2*time.Second))
	if rr[12] != 0 || !bytes.Equal(rr[13:16], []byte{0, 0, 0}) || binary.BigEndian.Uint32(rr[16:]) != 65537 {
		t.Fatalf("late packet report = %x", rr)
	}
	// The next interval loses one of two expected packets.
	r.record(155, 3, 3600, now.Add(40*time.Millisecond))
	rr = r.report(42, 155, now.Add(3*time.Second))
	if rr[12] != 128 || rr[15] != 1 {
		t.Fatalf("interval loss = %x, want fraction 128, cumulative 1", rr[12:16])
	}
	if got := binary.BigEndian.Uint32(rr[20:]); got == 0 {
		t.Fatal("variable transit did not produce jitter")
	}
	// A different source must not inherit sequence/loss/jitter or SR state.
	rr = r.report(42, 200, now.Add(4*time.Second))
	if len(rr) != 8 || r.haveSeq || r.lastSR != 0 || r.jitter != 0 {
		t.Fatalf("source change retained old state: %+v", r)
	}
}

func TestWatchReceptionTimestampWrap(t *testing.T) {
	now := time.Unix(100, 0)
	var r watchReception
	r.record(155, 100, ^uint32(0)-899, now)
	r.record(155, 101, 0, now.Add(10*time.Millisecond))
	if r.jitter != 0 {
		t.Fatalf("timestamp wrap jitter = %v, want 0", r.jitter)
	}
}

func TestWatchReceiveSenderReport(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			m := &media{}
			if err := m.useSecret(mode, [32]byte{}); err != nil {
				t.Fatal(err)
			}
			w := &Watch{aead: m.aead, videoSSRC: 155}
			now := time.Unix(100, 0)
			w.reception.record(155, 100, 1000, now)
			sr := make([]byte, 28)
			sr[0], sr[1] = 0x80, 200
			binary.BigEndian.PutUint16(sr[2:], 6)
			binary.BigEndian.PutUint32(sr[4:], 155)
			binary.BigEndian.PutUint32(sr[8:], 0x12345678)
			binary.BigEndian.PutUint32(sr[12:], 0x9abcdef0)
			// A trailing RR makes this a compound RTCP packet.
			compound := append(bytes.Clone(sr), 0x80, 201, 0, 1, 0, 0, 0, 155)
			pkt := bytes.Clone(m.seal(compound[:8], compound[8:]))
			w.receiveReport(pkt, now)
			rr := w.reception.report(42, 155, now.Add(1500*time.Millisecond))
			if got := binary.BigEndian.Uint32(rr[24:]); got != 0x56789abc {
				t.Fatalf("LSR = %x, want 56789abc", got)
			}
			if got := binary.BigEndian.Uint32(rr[28:]); got != 98304 {
				t.Fatalf("DLSR = %d, want 98304", got)
			}
			pkt[8] ^= 1
			w.receiveReport(pkt, now.Add(time.Second))
			if !w.reception.lastSRAt.Equal(now) {
				t.Fatal("unauthenticated SR changed reception state")
			}
			binary.BigEndian.PutUint32(sr[4:], 999)
			w.receiveReport(m.seal(sr[:8], sr[8:]), now.Add(time.Second))
			if !w.reception.lastSRAt.Equal(now) {
				t.Fatal("another SSRC's SR changed reception state")
			}
			w.receiveReport([]byte{0x80, 200}, now)
		})
	}
}
