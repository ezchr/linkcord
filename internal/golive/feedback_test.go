package golive

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestNACKPacking(t *testing.T) {
	want := []uint16{65534, 65535, 0, 1, 19, 20, 40}
	pkt := nackPacket(7, 9, want)
	if pkt[0] != 0x81 || pkt[1] != 205 ||
		int(binary.BigEndian.Uint16(pkt[2:])+1)*4 != len(pkt) ||
		binary.BigEndian.Uint32(pkt[4:]) != 7 ||
		binary.BigEndian.Uint32(pkt[8:]) != 9 {
		t.Fatalf("bad feedback header: %x", pkt)
	}
	if got := nackSequences(pkt[12:]); !slices.Equal(got, want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	if len(pkt) != 24 {
		t.Fatalf("feedback was not packed: %d bytes", len(pkt))
	}
}
