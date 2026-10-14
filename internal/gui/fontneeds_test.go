package gui

import "testing"

func TestDrawable(t *testing.T) {
	if got := drawable("🏝️ Game Room"); got != "🏝 Game Room" {
		t.Errorf("got %q", got)
	}
	if got := drawable("plain"); got != "plain" {
		t.Errorf("got %q", got)
	}
}
