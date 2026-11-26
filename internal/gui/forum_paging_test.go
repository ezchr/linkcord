package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestAppendThreadsDedups(t *testing.T) {
	got := appendThreads([]discord.Channel{{ID: 1}, {ID: 2}}, []discord.Channel{{ID: 2}, {ID: 3}, {ID: 3}})
	if len(got) != 3 || got[2].ID != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestPinnedFirst(t *testing.T) {
	got := pinnedFirst([]discord.Channel{{ID: 1}, {ID: 2, Flags: discord.PinnedThread}, {ID: 3}})
	if got[0].ID != 2 || got[1].ID != 1 || got[2].ID != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestToggleTagMax(t *testing.T) {
	var ct createThread
	for i := 1; i <= 7; i++ {
		ct.toggleTag(discord.TagID(i))
	}
	if len(ct.tags) != maxPostTags {
		t.Fatalf("got %d tags", len(ct.tags))
	}
	ct.toggleTag(1)
	if len(ct.tags) != maxPostTags-1 {
		t.Fatalf("toggle off failed")
	}
}
