package gui

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
)

// twemojiBase is where Discord's emoji pictures, Twemoji, are served.
const twemojiBase = "https://cdn.jsdelivr.net/gh/jdecked/twemoji@15.1.0/assets/"

// twemojiURL is the picture of the emoji e, shown px pixels wide.
func twemojiURL(e string, px int) string {
	zwj := strings.ContainsRune(e, 0x200D)
	var code []string
	for _, r := range e {
		// Twemoji names leave out the emoji style selector, except in
		// sequences joined with zero width joiners.
		if r == 0xFE0F && !zwj {
			continue
		}
		code = append(code, fmt.Sprintf("%x", r))
	}
	_ = px // 72 pixels: the largest PNG Twemoji has.
	return twemojiBase + "72x72/" + strings.Join(code, "-") + ".png"
}

type textPart struct {
	text  string
	emoji bool
}

// splitEmoji splits s into emoji, each a part of its own, and the text
// between them.
func splitEmoji(s string) []textPart {
	if isASCII(s) {
		return []textPart{{text: s}}
	}
	var parts []textPart
	var text strings.Builder
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		c := g.Str()
		if !isEmoji(c) {
			text.WriteString(c)
			continue
		}
		if text.Len() > 0 {
			parts = append(parts, textPart{text: text.String()})
			text.Reset()
		}
		parts = append(parts, textPart{text: c, emoji: true})
	}
	if text.Len() > 0 {
		parts = append(parts, textPart{text: text.String()})
	}
	return parts
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// isEmoji reports whether the grapheme c is an emoji Discord draws as a
// picture.
func isEmoji(c string) bool {
	if strings.ContainsRune(c, 0xFE0F) || strings.ContainsRune(c, 0x20E3) {
		return true
	}
	r := []rune(c)[0]
	switch {
	case r >= 0x1F1E6 && r <= 0x1F1FF: // flags
		return len([]rune(c)) >= 2
	case r >= 0x1F300 && r <= 0x1FAFF, r == 0x1F004, r == 0x1F0CF, r == 0x1F18E,
		r >= 0x1F191 && r <= 0x1F19A, r == 0x1F201, r == 0x1F21A, r == 0x1F22F,
		r >= 0x1F232 && r <= 0x1F236, r >= 0x1F238 && r <= 0x1F23A, r == 0x1F250, r == 0x1F251:
		return true
	}
	// The symbols drawn as emoji without a style selector.
	return strings.ContainsRune("⌚⌛⏩⏪⏫⏬⏰⏳◽◾☔☕♈♉♊♋♌♍♎♏♐♑♒♓♿⚓⚡⚪⚫⚽⚾⛄⛅⛎⛔⛪⛲⛳⛵⛺⛽✅✊✋✨❌❎❓❔❕❗➕➖➗➰➿⬛⬜⭐⭕", r)
}
