package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestDeletedForgetAndEdit(t *testing.T) {
	d := newDeletedLog("")
	d.record(discord.Message{ID: 1, ChannelID: 5, Content: "first"})
	d.record(discord.Message{ID: 2, ChannelID: 5, Content: "second"})

	d.setContent(1, "changed")
	if got := d.msgs[1].Message.Content; got != "changed" {
		t.Errorf("content %q", got)
	}
	d.forget(2)
	if d.has(2) {
		t.Error("forgotten message still kept")
	}
	// Deleted again on Discord, it is not kept again.
	d.record(discord.Message{ID: 2, ChannelID: 5, Content: "second"})
	if d.has(2) {
		t.Error("forgotten message kept again")
	}
}
