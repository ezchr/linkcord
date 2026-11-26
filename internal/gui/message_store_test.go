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

func TestRecentStoreKeepsMoreForOpenedChannels(t *testing.T) {
	s := newRecentStore(10, 5)
	s.background, s.opened = 4, 1
	for id := discord.MessageID(1); id <= 10; id++ {
		_ = s.MessageSet(&discord.Message{ID: id << 22, ChannelID: 1}, false)
		_ = s.MessageSet(&discord.Message{ID: id << 22, ChannelID: 2}, false)
		if id == 1 {
			s.MarkOpened(1)
		}
	}
	if msgs, _ := s.Messages(1); len(msgs) != 10 {
		t.Fatalf("opened channel kept %d messages, want 10", len(msgs))
	}
	if msgs, _ := s.Messages(2); len(msgs) != 4 || msgs[0].ID != 10<<22 {
		t.Fatalf("other channel kept %d messages, want the latest 4", len(msgs))
	}
	s.MarkOpened(2) // channel 1 is no longer opened and is cut down
	if msgs, _ := s.Messages(1); len(msgs) != 4 || msgs[0].ID != 10<<22 {
		t.Fatalf("channel 1 kept %d messages after closing, want the latest 4", len(msgs))
	}
}

func TestParsePage(t *testing.T) {
	g, c := parsePage(pageURL(12345, 67890))
	if g != 12345 || c != 67890 {
		t.Fatalf("parsePage = %v, %v", g, c)
	}
	if g, c := parsePage(pageURL(0, 5)); g.IsValid() || c != 5 {
		t.Fatalf("DM page = %v, %v", g, c)
	}
}
