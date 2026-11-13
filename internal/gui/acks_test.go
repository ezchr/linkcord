package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestBulkAcks(t *testing.T) {
	var chs []discord.ChannelID
	for i := 1; i <= 250; i++ {
		chs = append(chs, discord.ChannelID(i))
	}
	last := func(id discord.ChannelID) discord.MessageID {
		if id == 5 {
			return 0 // never had a message
		}
		return discord.MessageID(id * 10)
	}
	unread := func(id discord.ChannelID) bool { return id%2 == 1 || id > 100 }
	batches := bulkAcks(chs, last, unread)
	n := 0
	for _, b := range batches {
		if len(b) > 100 {
			t.Fatalf("batch of %d", len(b))
		}
		for _, a := range b {
			if a.ChannelID == 5 || !unread(a.ChannelID) || a.MessageID != discord.MessageID(a.ChannelID*10) || a.ReadStateType != 0 {
				t.Fatalf("wrong ack %+v", a)
			}
			n++
		}
	}
	// 49 odd channels up to 100 without 5, and the 150 after.
	if n != 199 || len(batches) != 2 {
		t.Fatalf("%d acks in %d batches", n, len(batches))
	}
	if bulkAcks(nil, last, unread) != nil {
		t.Fatal("acks for no channels")
	}
}
