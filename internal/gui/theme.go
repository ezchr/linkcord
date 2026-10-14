package gui

import (
	_ "embed"
	"encoding/binary"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/widget/material"
)

// Colors close to Discord's dark theme.
var (
	colorServers  = rgb(0x121214)
	colorSidebar  = rgb(0x1a1a1e)
	colorPanel    = rgb(0x161619)
	colorLink     = rgb(0x00a8fc)
	colorChat     = rgb(0x202024)
	colorInput    = rgb(0x2b2b30)
	colorHover    = rgb(0x29292e)
	colorSelected = rgb(0x35353c)
	colorText     = rgb(0xdbdee1)
	colorMuted    = rgb(0x949ba4)
	colorChannel  = rgb(0x80848e)
	colorHeader   = rgb(0xf2f3f5)
	colorAccent   = rgb(0x5865f2)
	colorGreen    = rgb(0x23a55a)
	// colorGreenButton is the green of filled buttons, such as Add Friend.
	colorGreenButton = rgb(0x248046)
	// colorBoost is the pink of server boosts, and colorBoostChip the purple
	// of the boost goal's label.
	colorBoost     = rgb(0xff73fa)
	colorBoostChip = rgb(0x5b2d7d)
	// colorSecondaryButton is the gray of secondary and link buttons.
	colorSecondaryButton = rgb(0x4e5058)
	colorYellow          = rgb(0xf0b232)
	colorOffline         = rgb(0x80848e)
	colorPopup           = rgb(0x111214)
	colorMenu            = rgb(0x18181c)
	colorRed             = rgb(0xda373c)
	colorCode            = rgb(0x18181c)
	colorMention         = rgb(0xc9cdfb)
	colorMentionB        = rgb(0x3c4270)
	// colorMentioned marks the messages that mention the user, as Discord's
	// gold.
	colorMentioned = rgb(0xf0b232)
)

type colorT = color.NRGBA

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

// colorEmoji is Noto Color Emoji, whose emoji are pictures, which the text
// shaper draws in color, unlike the vector color glyphs of Segoe UI Emoji.
// It is under the SIL Open Font License; see fonts/OFL.txt.
//
//go:embed fonts/NotoColorEmoji.ttf
var colorEmoji []byte

// Selected Windows fonts cover common scripts, symbols, and mathematical
// alphabets without loading the full system font catalog. Noto supplies emoji.
var fallbackFonts = []string{"segoeui.ttf", "seguisym.ttf", "cambria.ttc"}

// narrowSpace returns a copy of a font whose space glyph is a quarter of an
// em wide, as in text fonts. Noto Color Emoji has a space as wide as an
// emoji, and the shaper keeps the spaces that follow an emoji in its font,
// which left wide gaps after emoji.
func narrowSpace(font []byte) []byte {
	font = slices.Clone(font)
	be := binary.BigEndian
	table := func(tag string) int {
		if len(font) < 12 {
			return -1
		}
		for i := range int(be.Uint16(font[4:])) {
			rec := 12 + 16*i
			if rec+16 <= len(font) && string(font[rec:rec+4]) == tag {
				return int(be.Uint32(font[rec+8:]))
			}
		}
		return -1
	}
	cmap, head, hhea, hmtx := table("cmap"), table("head"), table("hhea"), table("hmtx")
	if cmap < 0 || head < 0 || hhea < 0 || hmtx < 0 || head+20 > len(font) || hhea+36 > len(font) {
		return font
	}
	// Find the glyph of the space in the format 12 character map.
	spaceGlyph := -1
	for j := range int(be.Uint16(font[cmap+2:])) {
		sub := cmap + int(be.Uint32(font[cmap+4+8*j+4:]))
		if sub+16 > len(font) || be.Uint16(font[sub:]) != 12 {
			continue
		}
		for g := range int(be.Uint32(font[sub+12:])) {
			grp := sub + 16 + 12*g
			if grp+12 > len(font) {
				break
			}
			if start, end := be.Uint32(font[grp:]), be.Uint32(font[grp+4:]); start <= ' ' && ' ' <= end {
				spaceGlyph = int(be.Uint32(font[grp+8:])) + int(' '-start)
			}
		}
	}
	unitsPerEm := int(be.Uint16(font[head+18:]))
	numMetrics := int(be.Uint16(font[hhea+34:]))
	if spaceGlyph < 0 || spaceGlyph >= numMetrics || hmtx+4*spaceGlyph+2 > len(font) {
		return font
	}
	be.PutUint16(font[hmtx+4*spaceGlyph:], uint16(unitsPerEm/4))
	return font
}

// newTheme uses bundled and selected fonts, with full system fallback
// for scripts not covered by the explicit collection.
func newTheme() *material.Theme {
	collection := gofont.Collection()
	if faces, err := opentype.ParseCollection(narrowSpace(colorEmoji)); err == nil {
		collection = append(collection, faces...)
	} else {
		slog.Warn("failed to parse the emoji font", "err", err)
	}
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	for _, name := range fallbackFonts {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(data)
		if err != nil {
			slog.Warn("failed to parse font", "font", name, "err", err)
			continue
		}
		collection = append(collection, faces...)
	}

	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(collection))
	th.Palette = material.Palette{
		Fg:         colorText,
		Bg:         colorChat,
		ContrastBg: colorAccent,
		ContrastFg: colorHeader,
	}
	// The typeface stays unset: naming one keeps bold text from falling
	// back to the fonts above.
	th.TextSize = 15
	return th
}
