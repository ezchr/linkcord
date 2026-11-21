package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestDeletedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deleted.json")
	c := &chatView{
		selChannel: &discord.Channel{ID: 10},
		deleted:    newDeletedLog(path),
		messages: []discord.Message{
			{ID: 100, ChannelID: 10, Content: "one"},
			{ID: 200, ChannelID: 10, Content: "two"},
			{ID: 300, ChannelID: 10, Content: "three"},
		},
	}
	// Someone else deletes "two": it stays, marked.
	c.apply(&gateway.MessageDeleteEvent{ID: 200, ChannelID: 10})
	if len(c.messages) != 3 || !c.deleted.has(200) {
		t.Fatalf("deleted message was not kept: %d messages, kept %v", len(c.messages), c.deleted.has(200))
	}
	// A deleted message whose contents were never seen just goes.
	c.apply(&gateway.MessageDeleteEvent{ID: 999, ChannelID: 10})
	if c.deleted.has(999) {
		t.Fatal("an unknown message was kept")
	}
	// The user's own deletions are not kept.
	c.deleted.deleting(300)
	c.apply(&gateway.MessageDeleteEvent{ID: 300, ChannelID: 10})
	if c.deleted.has(300) || len(c.messages) != 2 {
		t.Fatalf("own deletion was kept: %d messages", len(c.messages))
	}

	// It is saved, and returns in its place in history that no longer has it.
	c.deleted.flush() // written after a delay, or now when flushed
	deadline := time.Now().Add(2 * time.Second)
	var reloaded *deletedLog
	for time.Now().Before(deadline) {
		if reloaded = newDeletedLog(path); reloaded.has(200) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !reloaded.has(200) {
		t.Fatal("deleted message was not saved")
	}
	history := []discord.Message{{ID: 100, ChannelID: 10}, {ID: 300, ChannelID: 10}}
	merged := reloaded.merge(10, history, 0, 0)
	if len(merged) != 3 || merged[1].ID != 200 || merged[1].Content != "two" {
		t.Fatalf("merged history = %v", merged)
	}
	// Outside the loaded stretch, or in another channel, it stays out.
	if got := reloaded.merge(10, history, 250, 0); len(got) != 2 {
		t.Fatalf("message older than the loaded history was merged: %v", got)
	}
	if got := reloaded.merge(10, history, 0, 150); len(got) != 2 {
		t.Fatalf("message newer than the page was merged: %v", got)
	}
	if got := reloaded.merge(11, history, 0, 0); len(got) != 2 {
		t.Fatalf("message of another channel was merged: %v", got)
	}
}

// The log of old releases (a JSON array) is read, the log of lines is
// written beside it, and what is added and forgotten after stays so.
func TestDeletedLogLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deleted.json")
	old := []deletedEntry{{Message: discord.Message{ID: 1, ChannelID: 5, Content: "old"}, DeletedAt: time.Now()}}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	d := newDeletedLog(path)
	if !d.has(1) {
		t.Fatal("the old file was not read")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the old file was kept")
	}
	d.record(discord.Message{ID: 2, ChannelID: 5, Content: "two"})
	d.record(discord.Message{ID: 3, ChannelID: 5, Content: "three"})
	d.forget(2)
	d.setContent(3, "edited")
	d.flush()
	r := newDeletedLog(path)
	if !r.has(1) || r.has(2) || !r.has(3) {
		t.Fatalf("reloaded: 1 %v, 2 %v, 3 %v", r.has(1), r.has(2), r.has(3))
	}
	if got := r.merge(5, nil, 0, 0); len(got) != 2 || got[1].Content != "edited" {
		t.Fatalf("merged = %v", got)
	}
}
