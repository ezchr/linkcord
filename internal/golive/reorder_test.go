package golive

import (
	"slices"
	"testing"
	"time"
)

func TestPacketOrderRepairAndWrap(t *testing.T) {
	now := time.Unix(1, 0)
	var q packetOrder
	q.push(receivedPacket{seq: 65534, at: now})
	if p, ok := q.pop(now); !ok || p.seq != 65534 {
		t.Fatal("first packet was not delivered")
	}
	q.push(receivedPacket{seq: 0, at: now})
	q.push(receivedPacket{seq: 1, at: now})
	if got := q.missing(now); !slices.Equal(got, []uint16{65535}) {
		t.Fatalf("missing %v", got)
	}
	if _, ok := q.pop(now); ok {
		t.Fatal("delivered across an unrepaired gap")
	}
	if !q.push(receivedPacket{seq: 65535, at: now}) {
		t.Fatal("rejected repair")
	}
	for _, want := range []uint16{65535, 0, 1} {
		if p, ok := q.pop(now); !ok || p.seq != want {
			t.Fatalf("delivered %+v, want %d", p, want)
		}
	}
	if q.push(receivedPacket{seq: 0, at: now}) {
		t.Fatal("accepted a late duplicate")
	}
}

func TestPacketOrderExpiryAndDuplicates(t *testing.T) {
	now := time.Unix(1, 0)
	var q packetOrder
	q.push(receivedPacket{seq: 10, at: now})
	q.pop(now)
	q.push(receivedPacket{seq: 12, at: now})
	if q.push(receivedPacket{seq: 12, at: now}) {
		t.Fatal("accepted buffered duplicate")
	}
	if _, ok := q.pop(now.Add(reorderDelay - time.Nanosecond)); ok {
		t.Fatal("expired too early")
	}
	if p, ok := q.pop(now.Add(reorderDelay)); !ok || p.seq != 12 {
		t.Fatal("did not resume after an unrecoverable loss")
	}
}

func TestPacketOrderBound(t *testing.T) {
	now := time.Unix(1, 0)
	var q packetOrder
	q.push(receivedPacket{seq: 0, at: now})
	q.pop(now)
	for i := 2; i < reorderLimit+2; i++ {
		q.push(receivedPacket{seq: uint16(i), at: now})
	}
	if q.push(receivedPacket{seq: reorderLimit + 2, at: now}) {
		t.Fatal("queue exceeded bound")
	}
	if p, ok := q.pop(now); !ok || p.seq != 2 {
		t.Fatal("full queue did not make progress")
	}
}
