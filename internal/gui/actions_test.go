package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestApplyReaction(t *testing.T) {
	fire := discord.Emoji{Name: "🔥"}
	custom := discord.Emoji{ID: 42, Name: "pog"}
	count := func(m *discord.Message, e discord.Emoji) (int, bool) {
		for _, r := range m.Reactions {
			if emojiKey(r.Emoji) == emojiKey(e) {
				return r.Count, r.Me
			}
		}
		return 0, false
	}

	var m discord.Message
	applyReaction(&m, fire, true, false) // someone reacts
	if n, me := count(&m, fire); n != 1 || me {
		t.Fatalf("after another's reaction = %d, me %v; want 1, false", n, me)
	}

	// The user's reaction shows when made, and Discord's report of it must
	// not count it twice.
	applyReaction(&m, fire, true, true)
	applyReaction(&m, fire, true, true)
	if n, me := count(&m, fire); n != 2 || !me {
		t.Fatalf("after own reaction and its report = %d, me %v; want 2, true", n, me)
	}

	// Same when taking it away.
	applyReaction(&m, fire, false, true)
	applyReaction(&m, fire, false, true)
	if n, me := count(&m, fire); n != 1 || me {
		t.Fatalf("after removing own = %d, me %v; want 1, false", n, me)
	}

	// The last one gone removes the reaction.
	applyReaction(&m, fire, false, false)
	if len(m.Reactions) != 0 {
		t.Fatalf("reactions left = %v; want none", m.Reactions)
	}

	// Custom emoji are told apart by ID, not by name.
	applyReaction(&m, custom, true, true)
	applyReaction(&m, discord.Emoji{ID: 43, Name: "pog"}, true, false)
	if len(m.Reactions) != 2 {
		t.Fatalf("custom emoji with the same name merged: %v", m.Reactions)
	}
}

func TestEmojiOfCell(t *testing.T) {
	got := emojiOfCell(&emojiCell{text: "<a:dance:123> ", url: "x"})
	if got.ID != 123 || got.Name != "dance" || !got.Animated {
		t.Fatalf("custom = %+v", got)
	}
	if got := emojiOfCell(&emojiCell{text: "😀"}); got.Name != "😀" || got.IsCustom() {
		t.Fatalf("unicode = %+v", got)
	}
}
