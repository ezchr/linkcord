package gui

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconBack = mustIcon(icons.NavigationChevronLeft)
)

// menuCheck is the box at the right of a menu item that can be ticked.
type menuCheck int8

const (
	menuNoCheck menuCheck = iota
	menuUnchecked
	menuChecked
)

// checked is the box of an item that is ticked when on.
func checked(on bool) menuCheck {
	if on {
		return menuChecked
	}
	return menuUnchecked
}

// menuItem is a line of a menu.
type menuItem struct {
	// key tells the item's clicks from the others'; it is text if "".
	key  string
	text string
	// col is the color of the text, colorText if zero, and colorRed for
	// what cannot be undone.
	col   colorT
	check menuCheck
	// sub reports whether the item opens a list of its own, shown with a
	// chevron, and back whether it goes back from one.
	sub, back bool
	// sep is a line between groups of items, and nothing else.
	sep bool
	// title is a line of small text that names what follows.
	title bool
	act   func()
	// lead is drawn at the left of the text, such as the color of a role.
	lead layout.Widget
	// w lays out the item in place of its text, as for a slider; it is not
	// clickable.
	w layout.Widget
}

// menuState is a menu open at the pointer, over everything else.
type menuState struct {
	open bool
	pos  image.Point
	// items are what the menu lists. They are built for every frame, so that
	// the menu follows what changes while it is open.
	items func() []menuItem
	// width is how wide the menu is.
	width unit.Dp
	// stay keeps the menu open after the item clicked acts, as for one that
	// ticks a box or opens a list of its own.
	stay   bool
	clicks map[string]*widget.Clickable
	list   widget.List
}

// openMenu opens a menu at pos listing items.
func (c *chatView) openMenu(pos image.Point, width unit.Dp, items func() []menuItem) {
	m := &c.menu
	m.open, m.pos, m.width, m.items = true, pos, width, items
	m.list.Axis = layout.Vertical
	m.list.Position = layout.Position{}
	if m.clicks == nil {
		m.clicks = make(map[string]*widget.Clickable)
	}
	clear(m.clicks)
}

func (c *chatView) closeMenu() {
	c.menu.open, c.menu.items = false, nil
}

// layoutMenu draws the open menu, which a click anywhere else closes.
func (c *chatView) layoutMenu(gtx layout.Context) {
	m := &c.menu
	if !m.open || m.items == nil {
		return
	}
	th := c.u.th
	size := gtx.Constraints.Max
	// A press outside the menu, of any button, closes it.
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.open, Kinds: pointer.Press}); !ok {
			break
		}
		c.closeMenu()
		return
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.open)
	area.Pop()

	items := m.items()
	var act func()
	for _, it := range items {
		if it.sep || it.title || it.w != nil {
			continue
		}
		if clickOf(m.clicks, it.menuKey()).Clicked(gtx) && it.act != nil {
			act = it.act
		}
	}
	if act != nil {
		m.stay = false
		act()
		if !m.stay {
			c.closeMenu()
		}
		if !m.open {
			return
		}
		items = m.items()
	}

	margin := gtx.Dp(8)
	w := min(gtx.Dp(m.width), size.X-2*margin)
	mgtx := gtx
	mgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, size.Y-2*margin)}
	macro := op.Record(gtx.Ops)
	dims := rounded(mgtx, colorMenu, 6, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return listLayout(gtx, th, &m.list, len(items), func(gtx layout.Context, i int) layout.Dimensions {
				return c.layoutMenuItem(gtx, items[i])
			})
		})
	})
	call := macro.Stop()
	// The menu opens at the pointer, and toward the inside of the window
	// near its edges.
	pos := m.pos
	if pos.X+dims.Size.X > size.X-margin {
		pos.X = max(margin, pos.X-dims.Size.X)
	}
	if pos.Y+dims.Size.Y > size.Y-margin {
		pos.Y = max(margin, size.Y-margin-dims.Size.Y)
	}
	t := op.Offset(pos).Push(gtx.Ops)
	solid(gtx, &m.items, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

func (it menuItem) menuKey() string {
	if it.key != "" {
		return it.key
	}
	return it.text
}

// layoutMenuItem is a line of a menu, highlighted while hovered.
func (c *chatView) layoutMenuItem(gtx layout.Context, it menuItem) layout.Dimensions {
	th := c.u.th
	switch {
	case it.sep:
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, max(gtx.Dp(1), 1))
			paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: size}.Op())
			return layout.Dimensions{Size: size}
		})
	case it.title:
		return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(2), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, oneLine(bold(label(th, 12, colorMuted, it.text))).Layout)
	case it.w != nil:
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, it.w)
	}
	click := clickOf(c.menu.clicks, it.menuKey())
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		col := it.col
		if col == (colorT{}) {
			col = colorText
		}
		hovered := click.Hovered()
		if hovered {
			// Discord fills the item hovered with the accent, or red for
			// what cannot be undone, and writes it in white.
			bg := colorAccent
			if it.col == colorRed {
				bg = colorRed
			}
			return rounded(gtx, bg, 3, func(gtx layout.Context) layout.Dimensions {
				return c.menuItemRow(gtx, it, colorHeader, true)
			})
		}
		return c.menuItemRow(gtx, it, col, false)
	})
}

func (c *chatView) menuItemRow(gtx layout.Context, it menuItem, col colorT, hovered bool) layout.Dimensions {
	th := c.u.th
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !it.back {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icon(gtx, iconBack, 16, col)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if it.lead == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, it.lead)
			}),
			layout.Flexed(1, oneLine(label(th, 14, col, it.text)).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				switch {
				case it.sub:
					return icon(gtx, iconFold, 16, col)
				case it.check != menuNoCheck:
					return checkBox(gtx, it.check == menuChecked, hovered)
				}
				return layout.Dimensions{}
			}),
		)
	})
}

// checkBox is the box of a menu item that can be ticked, as Discord draws it.
func checkBox(gtx layout.Context, on, hovered bool) layout.Dimensions {
	s := gtx.Dp(18)
	r := image.Rectangle{Max: image.Pt(s, s)}
	if on {
		bg := colorAccent
		if hovered {
			bg = colorHeader
		}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(r, gtx.Dp(3)).Op(gtx.Ops))
		fg := colorHeader
		if hovered {
			fg = colorAccent
		}
		gtx.Constraints = layout.Exact(r.Max)
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconCheck, 14, fg) })
		return layout.Dimensions{Size: r.Max}
	}
	col := colorMuted
	if hovered {
		col = colorHeader
	}
	w := max(gtx.Dp(2), 1)
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.UniformRRect(r.Inset(w/2), gtx.Dp(3)).Path(gtx.Ops), Width: float32(w)}.Op())
	return layout.Dimensions{Size: r.Max}
}

// rightClicked reports whether the area of size at the origin was clicked
// with the right button, and has it watched for the next frame. Clicks
// still reach what is under it.
func rightClicked(gtx layout.Context, tag event.Tag, size image.Point) bool {
	clicked := false
	for {
		e, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press})
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok && pe.Buttons.Contain(pointer.ButtonSecondary) {
			clicked = true
		}
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
	return clicked
}

// trackPointer keeps where the pointer is in the window, for menus to open
// there. It is laid out over everything and lets the pointer through.
func (c *chatView) trackPointer(gtx layout.Context) {
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &c.pointerAt, Kinds: pointer.Move | pointer.Press | pointer.Drag})
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok {
			c.pointerAt = pe.Position.Round()
		}
	}
}

func (c *chatView) watchPointer(gtx layout.Context) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &c.pointerAt)
}

// rightTag returns the tag that watches right clicks on the thing named key.
func (c *chatView) rightTag(key string) *int {
	t, ok := c.rightTags[key]
	if !ok {
		t = new(int)
		c.rightTags[key] = t
	}
	return t
}
