//go:build windows && !no_voice

package golive

import (
	"encoding/binary"
	"slices"
	"time"
)

// Transport-wide congestion control, as WebRTC does it: every packet the
// stream server sends carries, in an RTP header extension, a number that
// counts all of them, and the receiver tells back, every 100 ms, which
// arrived and when (RTCP transport feedback, type 205 format 15). A
// streamer that hears nothing back takes the network as jammed and lowers
// the stream to nothing, a picture a second.
//
// https://datatracker.ietf.org/doc/html/draft-holmer-rmcat-transport-wide-cc-extensions-01

// twccEvery is how often transport feedback is sent, as WebRTC does.
const twccEvery = 100 * time.Millisecond

// twcc keeps which packets arrived when, for transport feedback.
type twcc struct {
	// id is the header extension that carries the number, found by
	// watching which one counts up one by one; 0 until found.
	id    int
	score map[int]int
	last  map[int]uint16

	// arrivals are when each packet not told of yet arrived, by number,
	// unwrapped; next is the first number not told of.
	arrivals map[int64]time.Time
	next     int64
	started  bool
	high     int64
	count    uint8
	epoch    time.Time
}

func newTWCC() *twcc {
	return &twcc{score: make(map[int]int), last: make(map[int]uint16), arrivals: make(map[int64]time.Time), epoch: time.Now()}
}

// observe reads the header extension of a packet that arrived at now:
// profile is its profile, 0xBEDE for one-byte elements, and ext its
// elements.
func (t *twcc) observe(profile uint16, ext []byte, now time.Time) {
	for id, v := range extElements(profile, ext) {
		if len(v) != 2 {
			continue
		}
		n := binary.BigEndian.Uint16(v)
		if t.id == 0 {
			// The number counts every packet: one element that goes up by
			// one, packet after packet, is it.
			if last, ok := t.last[id]; ok && n == last+1 {
				if t.score[id]++; t.score[id] >= 20 {
					t.id = id
				}
			} else if ok {
				t.score[id] = max(t.score[id]-2, 0)
			}
			t.last[id] = n
			continue
		}
		if id == t.id {
			t.arrived(n, now)
		}
	}
}

// arrived notes that the packet numbered n arrived at now.
func (t *twcc) arrived(n uint16, now time.Time) {
	var seq int64
	if !t.started {
		t.started, t.next, t.high, seq = true, int64(n), int64(n), int64(n)
	} else {
		// Unwrapped against the highest so far.
		seq = t.high + int64(int16(n-uint16(t.high)))
	}
	if seq < t.next {
		return // already told of, or very late
	}
	t.high = max(t.high, seq)
	if _, ok := t.arrivals[seq]; !ok {
		t.arrivals[seq] = now
	}
}

// feedback is the transport feedback of what arrived since the last, from
// sender about media, sealed by the caller; nil when there is nothing.
func (t *twcc) feedback(sender, media uint32) []byte {
	if t.id == 0 || len(t.arrivals) == 0 {
		return nil
	}
	seqs := make([]int64, 0, len(t.arrivals))
	for s := range t.arrivals {
		seqs = append(seqs, s)
	}
	slices.Sort(seqs)
	base, last := t.next, seqs[len(seqs)-1]
	// A report tells of 2^16 packets at most; far behind, the oldest are
	// given up.
	if last-base > 8000 {
		base = last - 8000
	}
	first := t.arrivals[seqs[0]]
	for _, s := range seqs {
		if s >= base {
			first = t.arrivals[s]
			break
		}
	}

	// The reference time counts 64 ms from an epoch of our own, and each
	// delta 250 µs from the arrival before.
	ref := int64(first.Sub(t.epoch) / (64 * time.Millisecond))
	prev := t.epoch.Add(time.Duration(ref) * 64 * time.Millisecond)
	var symbols []byte // 0 lost, 1 small delta, 2 large delta
	var deltas []byte
	for s := base; s <= last; s++ {
		at, ok := t.arrivals[s]
		if !ok {
			symbols = append(symbols, 0)
			continue
		}
		d := int64((at.Sub(prev) + 125*time.Microsecond) / (250 * time.Microsecond))
		if d >= 0 && d <= 255 {
			symbols = append(symbols, 1)
			deltas = append(deltas, byte(d))
		} else {
			d = max(-32768, min(32767, d))
			symbols = append(symbols, 2)
			deltas = binary.BigEndian.AppendUint16(deltas, uint16(int16(d)))
		}
		prev = prev.Add(time.Duration(d) * 250 * time.Microsecond)
	}

	pkt := make([]byte, 20, 20+len(symbols)/7*2+2+len(deltas)+3)
	binary.BigEndian.PutUint32(pkt[4:], sender)
	binary.BigEndian.PutUint32(pkt[8:], media)
	binary.BigEndian.PutUint16(pkt[12:], uint16(base))
	binary.BigEndian.PutUint16(pkt[14:], uint16(len(symbols)))
	pkt[16], pkt[17], pkt[18] = byte(ref>>16), byte(ref>>8), byte(ref)
	pkt[19] = t.count
	// Two-bit status vector chunks, seven statuses each.
	for i := 0; i < len(symbols); i += 7 {
		chunk := uint16(0b11 << 14)
		for j := 0; j < 7; j++ {
			var sym byte
			if i+j < len(symbols) {
				sym = symbols[i+j]
			}
			chunk |= uint16(sym) << (12 - 2*j)
		}
		pkt = binary.BigEndian.AppendUint16(pkt, chunk)
	}
	pkt = append(pkt, deltas...)
	// Padded to whole words, the last byte saying how many were added.
	if pad := (4 - len(pkt)%4) % 4; pad > 0 {
		pkt = append(pkt, make([]byte, pad)...)
		pkt[len(pkt)-1] = byte(pad)
		pkt[0] = 0xA0 | 15 // version 2, padding, format 15
	} else {
		pkt[0] = 0x80 | 15
	}
	pkt[1] = 205
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)/4-1))

	t.count++
	t.next = last + 1
	for s := range t.arrivals {
		if s <= last {
			delete(t.arrivals, s)
		}
	}
	return pkt
}

// extElements are the elements of an RTP header extension, by ID: of
// one-byte headers (profile 0xBEDE), or of two-byte ones (0x100x).
func extElements(profile uint16, ext []byte) map[int][]byte {
	els := make(map[int][]byte)
	switch {
	case profile == 0xBEDE:
		for i := 0; i < len(ext); {
			b := ext[i]
			id, n := int(b>>4), int(b&0x0F)+1
			if id == 0 {
				i++ // padding
				continue
			}
			if id == 15 || i+1+n > len(ext) {
				break
			}
			els[id] = ext[i+1 : i+1+n]
			i += 1 + n
		}
	case profile&0xFFF0 == 0x1000:
		for i := 0; i+1 < len(ext); {
			id, n := int(ext[i]), int(ext[i+1])
			if id == 0 {
				i++
				continue
			}
			if i+2+n > len(ext) {
				break
			}
			els[id] = ext[i+2 : i+2+n]
			i += 2 + n
		}
	}
	return els
}
