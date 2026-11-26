package golive

import (
	"encoding/binary"
	"testing"
)

func block(ssrc uint32, lost byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b, ssrc)
	b[4] = lost
	return b
}

func TestLossLowersAndRaisesBitrate(t *testing.T) {
	m := &media{}
	if got := m.targetBitrate(6_000_000); got != 6_000_000 {
		t.Fatalf("full rate at start: %d", got)
	}
	m.reportBlocks(block(7, 77), 1, 7) // 30% lost
	low := m.targetBitrate(6_000_000)
	if low >= 6_000_000*9/10 {
		t.Fatalf("30%% loss left the bitrate at %d", low)
	}
	m.reportBlocks(block(9, 200), 1, 7) // another stream's loss
	if m.targetBitrate(6_000_000) != low {
		t.Fatal("another SSRC's loss counted")
	}
	for range 50 {
		m.reportBlocks(block(7, 0), 1, 7)
	}
	if got := m.targetBitrate(6_000_000); got != 6_000_000 {
		t.Fatalf("no loss did not recover to full: %d", got)
	}
	m.remb.Store(2_000_000)
	if got := m.targetBitrate(6_000_000); got != 2_000_000 {
		t.Fatalf("REMB not honoured: %d", got)
	}
}
