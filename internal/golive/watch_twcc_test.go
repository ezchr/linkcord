//go:build windows && !no_voice

package golive

import (
	"encoding/binary"
	"testing"
	"time"
)

// TestTWCC finds the extension that counts packets, and tells back which
// arrived when, as the draft lays it out.
func TestTWCC(t *testing.T) {
	tw := newTWCC()
	start := time.Now()
	ext := func(n uint16) []byte {
		// Another element first (ID 3, 3 bytes), then the number (ID 5).
		e := []byte{0x32, 9, 9, 9, 0x51, 0, 0}
		binary.BigEndian.PutUint16(e[5:], n)
		return e
	}
	// 30 packets to find it, then 10 more, 3 of them lost.
	n := uint16(65530) // across the wrap
	for range 30 {
		tw.observe(0xBEDE, ext(n), start)
		n++
	}
	if tw.id != 5 {
		t.Fatalf("found extension %d, not 5", tw.id)
	}
	tw.feedback(1, 2) // what was seen while looking
	lost := map[int]bool{2: true, 3: true, 7: true}
	var sent []time.Time
	first := n
	for i := range 10 {
		at := start.Add(time.Second + time.Duration(i)*7*time.Millisecond)
		sent = append(sent, at)
		if !lost[i] {
			tw.observe(0xBEDE, ext(n), at)
		}
		n++
	}
	pkt := tw.feedback(1, 2)
	if pkt == nil || len(pkt)%4 != 0 {
		t.Fatalf("feedback %x", pkt)
	}
	if pkt[1] != 205 || pkt[0]&0x1F != 15 || int(binary.BigEndian.Uint16(pkt[2:])) != len(pkt)/4-1 {
		t.Fatalf("header %x", pkt[:4])
	}
	base, count := binary.BigEndian.Uint16(pkt[12:]), int(binary.BigEndian.Uint16(pkt[14:]))
	// The last packet, 9, arrived; 7 did not, so the report runs to 9.
	if base != first || count != 10 {
		t.Fatalf("base %d count %d, want %d and 10", base, count, first)
	}
	ref := int64(pkt[16])<<16 | int64(pkt[17])<<8 | int64(pkt[18])
	var symbols []int
	at := 20
	for len(symbols) < count {
		chunk := binary.BigEndian.Uint16(pkt[at:])
		at += 2
		for j := 0; j < 7; j++ {
			symbols = append(symbols, int(chunk>>(12-2*j))&3)
		}
	}
	clock := tw.epoch.Add(time.Duration(ref) * 64 * time.Millisecond)
	for i := range count {
		if lost[i] {
			if symbols[i] != 0 {
				t.Errorf("packet %d lost, said %d", i, symbols[i])
			}
			continue
		}
		var d int
		switch symbols[i] {
		case 1:
			d = int(pkt[at])
			at++
		case 2:
			d = int(int16(binary.BigEndian.Uint16(pkt[at:])))
			at += 2
		default:
			t.Fatalf("packet %d arrived, said lost", i)
		}
		clock = clock.Add(time.Duration(d) * 250 * time.Microsecond)
		if diff := clock.Sub(sent[i]); diff > 250*time.Microsecond || diff < -250*time.Microsecond {
			t.Errorf("packet %d arrived at %v, told %v", i, sent[i].Sub(start), clock.Sub(start))
		}
	}
	if tw.feedback(1, 2) != nil {
		t.Error("told twice")
	}
}
