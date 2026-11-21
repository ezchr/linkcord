package gui

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestRecentStoreLetsOldChannelsGo(t *testing.T) {
	s := newRecentStore(10, 3)
	add := func(ch discord.ChannelID, id discord.MessageID) {
		m := &discord.Message{ID: id, ChannelID: ch, Timestamp: discord.NewTimestamp(time.Unix(int64(id), 0))}
		if err := s.MessageSet(m, false); err != nil {
			t.Fatal(err)
		}
	}
	add(1, 1)
	add(2, 2)
	add(3, 3)
	add(1, 4) // channel 1 is the latest again
	add(4, 5) // over three channels: channel 2, the oldest, goes
	if msgs, _ := s.Messages(2); len(msgs) != 0 {
		t.Fatalf("channel 2 kept %d messages", len(msgs))
	}
	for _, ch := range []discord.ChannelID{1, 3, 4} {
		if msgs, _ := s.Messages(ch); len(msgs) == 0 {
			t.Fatalf("channel %d lost its messages", ch)
		}
	}
	if m, err := s.Message(1, 4); err != nil || m.ID != 4 {
		t.Fatalf("message 4 = %v, %v", m, err)
	}
}
