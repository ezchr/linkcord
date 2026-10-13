package gui

import (
	"image"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/x/styledtext"
)

// textPos is a place in the formatted text of a message: before the rune
// rune of the span span.
type textPos struct {
	span, rune int
}

func (p textPos) before(q textPos) bool {
	return p.span < q.span || p.span == q.span && p.rune < q.rune
}

// textSelection is the text selected with the mouse, in one formatted text:
// a message, an embed's description…
type textSelection struct {
	state          *richtextState
	anchor, focus  textPos
	dragging       bool
	lastPress      time.Time
	lastPressState *richtextState
	presses        int
}

// bounds returns the selection from its start to its end.
func (s *textSelection) bounds() (textPos, textPos) {
	if s.focus.before(s.anchor) {
		return s.focus, s.anchor
	}
	return s.anchor, s.focus
}

func (s *textSelection) empty() bool {
	return s.state == nil || s.anchor == s.focus
}

// doubleClick is how close two presses must be to make a double click.
const doubleClick = 400 * time.Millisecond

// updateSelection selects the text of state that the mouse drags over: a
// word with a double click, and all of it with a triple click. A right
// click on what is selected opens a menu to copy it.
func (c *chatView) updateSelection(gtx layout.Context, state *richtextState) {
	sel := &c.sel
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: state, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		pos := state.hit(pe.Position.Round())
		switch pe.Kind {
		case pointer.Press:
			if pe.Buttons.Contain(pointer.ButtonSecondary) {
				if sel.state == state && !sel.empty() {
					c.openMenu(c.pointerAt, 160, func() []menuItem {
						return []menuItem{{text: "Copy", act: c.copySelection}}
					})
				}
				continue
			}
			if !pe.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			if sel.lastPressState == state && gtx.Now.Sub(sel.lastPress) < doubleClick {
				sel.presses++
			} else {
				sel.presses = 1
			}
			sel.lastPress, sel.lastPressState = gtx.Now, state
			sel.state, sel.anchor, sel.focus, sel.dragging = state, pos, pos, true
			switch {
			case sel.presses == 2:
				sel.anchor, sel.focus = state.word(pos)
				sel.dragging = false
			case sel.presses >= 3:
				sel.anchor, sel.focus = state.whole()
				sel.dragging = false
			}
			// The keyboard comes to the selection, for Ctrl+C to copy it.
			gtx.Execute(key.FocusCmd{Tag: sel})
		case pointer.Drag:
			if sel.dragging && sel.state == state {
				sel.focus = pos
			}
		case pointer.Release, pointer.Cancel:
			sel.dragging = false
		}
	}
}

// updateSelectionKeys copies what is selected with Ctrl+C and forgets it
// with Escape or when the keyboard goes elsewhere.
func (c *chatView) updateSelectionKeys(gtx layout.Context) {
	sel := &c.sel
	event.Op(gtx.Ops, sel)
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: sel},
			key.Filter{Focus: sel, Name: "C", Required: key.ModShortcut},
			key.Filter{Focus: sel, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.FocusEvent:
			if !e.Focus {
				sel.state = nil
			}
		case key.Event:
			if e.State != key.Press {
				continue
			}
			if e.Name == key.NameEscape {
				sel.state = nil
				continue
			}
			c.copySelection()
		}
	}
}

// copySelection copies the text selected.
func (c *chatView) copySelection() {
	if text := c.sel.text(); text != "" {
		c.copyText = text
	}
}

// text is what is selected, as it reads: server emoji by their names.
func (s *textSelection) text() string {
	if s.empty() {
		return ""
	}
	a, b := s.bounds()
	all := s.state.all
	var out strings.Builder
	for i := a.span; i <= b.span && i < len(all); i++ {
		span := all[i]
		runes := []rune(span.Content)
		lo, hi := 0, len(runes)
		if i == a.span {
			lo = min(a.rune, hi)
		}
		if i == b.span {
			hi = min(b.rune, hi)
		}
		if lo >= hi {
			continue
		}
		if alt, _ := span.Get(metaCopy).(string); alt != "" {
			out.WriteString(alt)
			continue
		}
		out.WriteString(string(runes[lo:hi]))
	}
	return out.String()
}

// hit returns the place in the text nearest to p, in the coordinates of the
// whole text.
func (s *richtextState) hit(p image.Point) textPos {
	if len(s.pieces) == 0 {
		return textPos{}
	}
	first, last := s.pieces[0], s.pieces[len(s.pieces)-1]
	if p.Y < first.Rect.Min.Y {
		return textPos{first.Index, first.Carets[0].Rune}
	}
	if p.Y >= last.Rect.Max.Y {
		return textPos{last.Index, last.Carets[len(last.Carets)-1].Rune}
	}
	// The line nearest to p, then the piece of it nearest to p.
	best, bestDY, bestDX := 0, -1, -1
	for i, pc := range s.pieces {
		dy := 0
		switch {
		case p.Y < pc.Rect.Min.Y:
			dy = pc.Rect.Min.Y - p.Y
		case p.Y >= pc.Rect.Max.Y:
			dy = p.Y - pc.Rect.Max.Y + 1
		}
		dx := 0
		switch {
		case p.X < pc.Rect.Min.X:
			dx = pc.Rect.Min.X - p.X
		case p.X >= pc.Rect.Max.X:
			dx = p.X - pc.Rect.Max.X + 1
		}
		if bestDY < 0 || dy < bestDY || dy == bestDY && dx < bestDX {
			best, bestDY, bestDX = i, dy, dx
		}
	}
	pc := s.pieces[best]
	x := p.X - pc.Rect.Min.X
	caret := pc.Carets[0]
	for _, cr := range pc.Carets {
		if abs(cr.X-x) < abs(caret.X-x) {
			caret = cr
		}
	}
	return textPos{pc.Index, caret.Rune}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// word returns the bounds of the word at p, or of the space or mark there.
func (s *richtextState) word(p textPos) (textPos, textPos) {
	if p.span >= len(s.all) {
		return p, p
	}
	runes := []rune(s.all[p.span].Content)
	if len(runes) == 0 {
		return p, p
	}
	at := min(p.rune, len(runes)-1)
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	kind := isWord(runes[at])
	lo, hi := at, at+1
	for lo > 0 && isWord(runes[lo-1]) == kind && !unicode.IsSpace(runes[lo-1]) {
		lo--
	}
	for hi < len(runes) && isWord(runes[hi]) == kind && !unicode.IsSpace(runes[hi]) {
		hi++
	}
	return textPos{p.span, lo}, textPos{p.span, hi}
}

// whole returns the bounds of all the text.
func (s *richtextState) whole() (textPos, textPos) {
	n := len(s.all)
	if n == 0 {
		return textPos{}, textPos{}
	}
	return textPos{}, textPos{n - 1, utf8.RuneCountInString(s.all[n-1].Content)}
}

// layoutSelection lights what is selected of state, behind its text.
func (c *chatView) layoutSelection(gtx layout.Context, state *richtextState) {
	sel := &c.sel
	if sel.state != state || sel.empty() {
		return
	}
	a, b := sel.bounds()
	col := withAlpha(colorAccent, 0x66)
	for _, pc := range state.pieces {
		r0, r1 := pc.Carets[0].Rune, pc.Carets[len(pc.Carets)-1].Rune
		lo, hi := r0, r1
		switch {
		case pc.Index < a.span || pc.Index > b.span:
			continue
		case pc.Index == a.span:
			lo = max(r0, a.rune)
		}
		if pc.Index == b.span {
			hi = min(r1, b.rune)
		}
		if lo >= hi {
			continue
		}
		x0, x1 := caretX(pc, lo), caretX(pc, hi)
		r := image.Rect(pc.Rect.Min.X+x0, pc.Rect.Min.Y, pc.Rect.Min.X+x1, pc.Rect.Max.Y)
		paint.FillShape(gtx.Ops, col, clip.Rect(r).Op())
	}
}

// caretX is where the rune rune of a piece starts, from its left.
func caretX(pc styledtext.SpanPiece, rune int) int {
	x := 0
	for _, cr := range pc.Carets {
		if cr.Rune > rune {
			break
		}
		x = cr.X
	}
	return x
}
