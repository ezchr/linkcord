package gui

import (
	"slices"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

// A server dragged out of a folder of two onto that folder's own icon stays
// beside the other one: the folder is gone by then, and the server used to
// land at the end of the list.
func TestDropOutOfFolderOfTwo(t *testing.T) {
	c := &chatView{demo: true, openFolders: map[int64]bool{7: true}}
	c.folders = []serverFolder{
		{ID: 7, Guilds: []discord.GuildID{1, 2}},
		{Guilds: []discord.GuildID{3}},
		{Guilds: []discord.GuildID{4}},
	}
	entries := []guildEntry{
		{folder: &c.folders[0]},
		{folder: &c.folders[0], guild: 1},
		{folder: &c.folders[0], guild: 2},
		{guild: 3},
		{guild: 4},
	}
	c.dropGuild(entries, 2, 0, -1)
	if want := []discord.GuildID{2, 1, 3, 4}; !slices.Equal(c.guildOrder, want) {
		t.Fatalf("order %v, want %v", c.guildOrder, want)
	}
	for _, f := range c.folders {
		if f.ID != 0 {
			t.Fatalf("a folder of one is left: %+v", f)
		}
	}
}
