package gui

import (
	"slices"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestRangeIDs(t *testing.T) {
	order := []discord.MessageID{10, 20, 30, 40, 50}
	tests := []struct {
		name           string
		anchor, target discord.MessageID
		want           []discord.MessageID
	}{
		{"down", 20, 40, []discord.MessageID{20, 30, 40}},
		{"up", 40, 10, []discord.MessageID{10, 20, 30, 40}},
		{"the same", 30, 30, []discord.MessageID{30}},
		{"the anchor is gone", 99, 30, []discord.MessageID{30}},
		{"no anchor", 0, 50, []discord.MessageID{50}},
		{"the target is gone", 20, 99, nil},
	}
	for _, tt := range tests {
		if got := rangeIDs(order, tt.anchor, tt.target); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A selection takes up to bulkLimit messages; ticking one selected takes it
// out.
func TestMultiSelectLimit(t *testing.T) {
	var s multiSelect
	if !s.toggle(1) || !s.ids[1] {
		t.Fatal("the first message was not selected")
	}
	if !s.toggle(1) || s.ids[1] {
		t.Fatal("ticking it again did not take it out")
	}
	var ids []discord.MessageID
	for i := range bulkLimit + 10 {
		ids = append(ids, discord.MessageID(100+i))
	}
	if s.add(ids...) {
		t.Error("more than the limit fit")
	}
	if len(s.ids) != bulkLimit {
		t.Errorf("%d selected, want %d", len(s.ids), bulkLimit)
	}
	// Those that fit are the first of the range.
	if !s.ids[100] || !s.ids[discord.MessageID(100+bulkLimit-1)] || s.ids[discord.MessageID(100+bulkLimit)] {
		t.Error("not the first of them were selected")
	}
	if s.toggle(999) {
		t.Error("one more was selected at the limit")
	}
	// One of them can still be taken out, and selecting them again is fine.
	if !s.toggle(100) || !s.add(101, 102) {
		t.Error("could not take one out at the limit")
	}
	if !s.toggle(999) {
		t.Error("no room after taking one out")
	}
}

func TestSelectableMessage(t *testing.T) {
	plain := discord.Message{ID: 1}
	if !selectableMessage(plain, false) {
		t.Error("a plain message cannot be selected")
	}
	if selectableMessage(plain, true) {
		t.Error("a message deleted and kept can be selected")
	}
	join := plain
	join.Type = discord.GuildMemberJoinMessage
	if selectableMessage(join, false) {
		t.Error("a system message can be selected")
	}
	eph := plain
	eph.Flags = discord.EphemeralMessage
	if selectableMessage(eph, false) {
		t.Error("a message only the user sees can be selected")
	}
}

// Only the user's own messages can be deleted, or any where they manage
// messages; the others are counted as skipped.
func TestSplitDeletable(t *testing.T) {
	const me, other = discord.UserID(1), discord.UserID(2)
	msgs := []discord.Message{
		{ID: 1, Author: discord.User{ID: me}},
		{ID: 2, Author: discord.User{ID: other}},
		{ID: 3, Author: discord.User{ID: me}},
		{ID: 4, Author: discord.User{ID: other}, WebhookID: 9},
	}
	ids := func(ms []discord.Message) []discord.MessageID {
		var out []discord.MessageID
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}
	del, skipped := splitDeletable(msgs, me, false)
	if got := ids(del); !slices.Equal(got, []discord.MessageID{1, 3}) || skipped != 2 {
		t.Errorf("without managing: delete %v, skip %d", got, skipped)
	}
	del, skipped = splitDeletable(msgs, me, true)
	if len(del) != 4 || skipped != 0 {
		t.Errorf("managing messages: delete %v, skip %d", ids(del), skipped)
	}
	del, skipped = splitDeletable(msgs[1:2], me, false)
	if len(del) != 0 || skipped != 1 {
		t.Errorf("another's message: delete %v, skip %d", ids(del), skipped)
	}
	if !canDeleteMessage(msgs[0], me, false) || canDeleteMessage(msgs[1], me, false) || !canDeleteMessage(msgs[1], me, true) {
		t.Error("canDeleteMessage disagrees")
	}
}

func TestCopyLines(t *testing.T) {
	at := time.Date(2026, 3, 5, 14, 7, 0, 0, time.Local)
	msgs := []discord.Message{
		{ID: 1, Author: discord.User{Username: "ana"}, Content: "hello", Timestamp: discord.NewTimestamp(at)},
		{ID: 2, Author: discord.User{Username: "beto"}, Timestamp: discord.NewTimestamp(at.Add(time.Minute)),
			Attachments: []discord.Attachment{{URL: "https://cdn.example/a.png"}}},
		{ID: 3, Author: discord.User{Username: "ana"}, Content: "two\nlines", Timestamp: discord.NewTimestamp(at.Add(2 * time.Minute))},
	}
	got := copyLines(msgs,
		func(m discord.Message) string { return m.Author.Username },
		func(m discord.Message) string { return m.Content })
	want := "ana — 05/03/2026 14:07\nhello\n\n" +
		"beto — 05/03/2026 14:08\nhttps://cdn.example/a.png\n\n" +
		"ana — 05/03/2026 14:09\ntwo\nlines"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := copyLines(nil, nil, nil); got != "" {
		t.Errorf("nothing selected copies %q", got)
	}
}
