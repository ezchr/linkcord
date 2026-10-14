package gui

import (
	"strings"
	"sync/atomic"
)

// The fonts for symbols and for Japanese, Korean and Chinese take much
// memory once read, so they are read only when text shown needs them:
// noteText marks what text needs, and the shaper is made again with them.

const (
	needSymbols = 1 << iota
	needCJK
	needKorean
	needIndic
	needThai
	needMyanmar
	needEthiopic
)

var (
	// fontNeeds are the fonts text has needed, fontsLoaded those in the
	// shaper.
	fontNeeds, fontsLoaded atomic.Uint32
	// onFontNeeds makes the shaper again, once text needs more fonts.
	onFontNeeds func()
)

// drawable is s without the emoji variation selectors, U+FE0E and U+FE0F,
// which only say how to draw what comes before them. No font Linkcord has
// holds them, and Gio would look through every font of Windows for one,
// reading a whole font of a hundred megabytes for an invisible mark.
func drawable(s string) string {
	for i := 0; i < len(s); i++ {
		// Both are encoded EF B8 8E/8F.
		if s[i] == 0xef && i+2 < len(s) && s[i+1] == 0xb8 && (s[i+2] == 0x8e || s[i+2] == 0x8f) {
			return strings.NewReplacer("︎", "", "️", "").Replace(s)
		}
	}
	return s
}

// noteText marks the fonts s needs. It is quick for the plain text most
// is.
func noteText(s string) {
	for i := 0; i < len(s); i++ {
		// Indic, Thai and Ethiopic letters start with E0 and E1.
		if s[i] >= 0xe0 {
			noteRunes(s[i:])
			return
		}
	}
}

func noteRunes(s string) {
	var need uint32
	for _, r := range s {
		switch {
		case r >= 0x0900 && r <= 0x0DFF:
			need |= needIndic
		case r >= 0x0E00 && r <= 0x0EFF:
			need |= needThai
		case r >= 0x1000 && r <= 0x109F:
			need |= needMyanmar
		case r >= 0x1200 && r <= 0x139F:
			need |= needEthiopic
		case r < 0x2100:
		case r >= 0x1100 && r <= 0x11FF, r >= 0xA960 && r <= 0xA97F, r >= 0xAC00 && r <= 0xD7FF, r >= 0x3130 && r <= 0x318F:
			need |= needKorean
		case isCJKRune(r):
			need |= needCJK
		case r >= 0x1F000 && r <= 0x1FAFF, r >= 0xFE00 && r <= 0xFE0F, r == 0x200D:
			// Emoji, which the emoji font has.
		case r <= 0x2BFF, r >= 0x1D400 && r <= 0x1D7FF:
			need |= needSymbols
		}
	}
	if need == 0 || fontsLoaded.Load()&need == need {
		return
	}
	if old := fontNeeds.Or(need); old|need != old && onFontNeeds != nil {
		onFontNeeds()
	}
}

func isCJKRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x11FF, r >= 0x2E80 && r <= 0x2FDF, r >= 0x3000 && r <= 0x9FFF,
		r >= 0xA960 && r <= 0xA97F, r >= 0xAC00 && r <= 0xD7FF, r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFFEF, r >= 0x20000 && r <= 0x3FFFF:
		return true
	}
	return false
}
