package gui

import (
	"testing"

	"gioui.org/font"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

// TestEmojiSpace checks that a space after an emoji is as narrow as any other
// space, rather than the wide one of the emoji font.
func TestEmojiSpace(t *testing.T) {
	sh := newTheme().Shaper
	width := func(s string) fixed.Int26_6 {
		sh.LayoutString(text.Parameters{Font: font.Font{}, PxPerEm: fixed.I(16), MaxWidth: 1000}, s)
		var w fixed.Int26_6
		for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
			w += g.Advance
		}
		return w
	}
	space := width("a b") - width("ab")
	afterEmoji := width("👋 a") - width("👋a")
	if afterEmoji > space+fixed.I(1) {
		t.Fatalf("space after an emoji is %v wide; want about %v", afterEmoji, space)
	}
	if width("👋") == 0 {
		t.Fatal("emoji has no width; the emoji font did not load")
	}
}
