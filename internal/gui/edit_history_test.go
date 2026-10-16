package gui

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestEditHistoryKept(t *testing.T) {
	sent := discord.NewTimestamp(time.Unix(1000, 0))
	c := &chatView{
		u: &ui{focused: true}, demo: true, selGuild: 1,
		selChannel: &discord.Channel{ID: 10},
		me:         discord.User{ID: 1},
		messages:   []discord.Message{{ID: 5, ChannelID: 10, Content: "hola", Timestamp: sent}},
	}
	edit := func(content string, at int64) {
		c.apply(&gateway.MessageUpdateEvent{Message: discord.Message{
			ID: 5, ChannelID: 10, Content: content, Timestamp: sent, EditedTimestamp: discord.NewTimestamp(time.Unix(at, 0)),
		}})
	}
	edit("hola, ¿qué tal?", 1100)
	edit("adiós", 1200)
	// An update that leaves the text, as embeds arriving, is no edit.
	edit("adiós", 1300)
	got := c.edits[5]
	if len(got) != 2 || got[0].content != "hola" || got[1].content != "hola, ¿qué tal?" {
		t.Fatalf("history: %+v", got)
	}
	if !got[0].at.Equal(sent.Time()) || !got[1].at.Equal(time.Unix(1100, 0)) {
		t.Fatalf("times: %v %v", got[0].at, got[1].at)
	}
	if c.messages[0].Content != "adiós" {
		t.Fatalf("message not updated: %q", c.messages[0].Content)
	}

	c.u.app.NoEditHistory = true
	edit("otra cosa", 1400)
	if len(c.edits[5]) != 2 {
		t.Fatal("history kept while turned off")
	}
}
