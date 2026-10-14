package gui

import (
	"image"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// paintComposerMentions draws the mention box behind each @name in the box
// to write that was picked from the list, so it shows it will mention.
func (c *chatView) paintComposerMentions(gtx layout.Context, size image.Point) {
	cs := &c.comp
	if len(cs.mentions) == 0 {
		return
	}
	txt := c.composer.Text()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	var regions []widget.Region
	for _, r := range mentionRanges(txt, cs.mentions) {
		regions = c.composer.Regions(r[0], r[1], regions[:0])
		for _, reg := range regions {
			b := reg.Bounds
			b.Min.X -= gtx.Dp(2)
			b.Max.X += gtx.Dp(2)
			paint.FillShape(gtx.Ops, colorMentionB, clip.UniformRRect(b, gtx.Dp(3)).Op(gtx.Ops))
		}
	}
}

// mentionRanges are the rune ranges of the picked mentions in txt: each
// @name of mentions, not followed by more of a name.
func mentionRanges(txt string, mentions map[string]string) [][2]int {
	var out [][2]int
	for name := range mentions {
		for from := 0; ; {
			i := strings.Index(txt[from:], name)
			if i < 0 {
				break
			}
			at := from + i
			end := at + len(name)
			from = end
			if next, _ := utf8.DecodeRuneInString(txt[end:]); end < len(txt) && (unicode.IsLetter(next) || unicode.IsDigit(next) || next == '_') {
				continue
			}
			start := utf8.RuneCountInString(txt[:at])
			out = append(out, [2]int{start, start + utf8.RuneCountInString(name)})
		}
	}
	return out
}
