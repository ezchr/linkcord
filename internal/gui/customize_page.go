package gui

import (
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// customizeView is the state of the Layout section of the Appearance page.
type customizeView struct {
	// places are the buttons of the bars' places, by bar and place.
	places      map[string]*widget.Clickable
	resetLayout widget.Clickable
}

// barChoice is a bar the user can move, and where it can go.
type barChoice struct {
	key, name string
	places    []string
	field     func(*paneLayout) *string
}

var barChoices = []barChoice{
	{"servers", "Server List", []string{placeLeft, placeRight, placeTop, placeBottom}, func(l *paneLayout) *string { return &l.Servers }},
	{"channels", "Channel List", []string{placeLeft, placeRight}, func(l *paneLayout) *string { return &l.Channels }},
	{"members", "Member List", []string{placeLeft, placeRight}, func(l *paneLayout) *string { return &l.Members }},
	{"panel", "User Panel", []string{placeTop, placeBottom}, func(l *paneLayout) *string { return &l.UserPanel }},
}

// placeNames are the places as the buttons name them.
var placeNames = map[string]string{placeLeft: "Left", placeRight: "Right", placeTop: "Top", placeBottom: "Bottom"}

// refreshColorEditor shows the color being edited as it is now.
func (c *chatView) refreshColorEditor() {
	av := &c.settings.look
	if tc := themeColorOf(av.editing); tc != nil {
		av.hex.SetText(hexOf(*tc.v))
		av.picker.set(*tc.v)
	}
}

func (c *chatView) updateCustomize(gtx layout.Context) {
	u := c.u
	cv := &c.settings.look.custom
	if cv.places == nil {
		cv.places = make(map[string]*widget.Clickable)
	}
	l := &u.appearance.Layout
	for _, b := range barChoices {
		for _, place := range b.places {
			if clickOf(cv.places, b.key+"/"+place).Clicked(gtx) {
				*l = l.normal()
				*b.field(l) = place
				*l = l.saved()
				c.themeChanged(true)
			}
		}
	}
	if cv.resetLayout.Clicked(gtx) {
		*l = paneLayout{}
		c.panes = paneSettings{Channels: 240, Members: 240}
		if !c.demo {
			c.savePanes()
		}
		c.themeChanged(true)
	}
}

// customizeRows are the Layout section of the Appearance page.
func (c *chatView) customizeRows() []layout.Widget {
	th := c.u.th
	cv := &c.settings.look.custom
	rows := []layout.Widget{
		settingsHeading(th, "LAYOUT"),
		settingsText(th, "Where each bar of the window goes."),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					var children []layout.FlexChild
					for _, b := range barChoices {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutBarChoice(gtx, b)
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Rigid(c.layoutLayoutDiagram),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &cv.resetLayout, colorSecondaryButton, "Reset Layout")
				})
			})
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
	}
	return rows
}

// layoutBarChoice is a bar's name and a button for each place it can go,
// the one chosen filled.
func (c *chatView) layoutBarChoice(gtx layout.Context, b barChoice) layout.Dimensions {
	th := c.u.th
	cv := &c.settings.look.custom
	l := c.paneLayout()
	chosen := *b.field(&l)
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(120)
			return label(th, 15, colorHeader, b.name).Layout(gtx)
		}),
	}
	for _, place := range b.places {
		cl := clickOf(cv.places, b.key+"/"+place)
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			bg := colorInput
			switch {
			case place == chosen:
				bg = colorAccent
			case cl.Hovered():
				bg = colorHover
			}
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx,
							label(th, 14, colorHeader, placeNames[place]).Layout)
					})
				})
			})
		}))
	}
	return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// layoutLayoutDiagram is the window drawn small, with the bars where they
// are, in the colors of the theme.
func (c *chatView) layoutLayoutDiagram(gtx layout.Context) layout.Dimensions {
	size := image.Pt(gtx.Dp(200), gtx.Dp(125))
	l := c.paneLayout()
	r := placePanes(l, image.Rectangle{Max: size}, gtx.Dp(14), gtx.Dp(44), gtx.Dp(38), true)
	rad := gtx.Dp(6)
	defer clip.UniformRRect(image.Rectangle{Max: size}, rad).Push(gtx.Ops).Pop()
	fill := func(rect image.Rectangle, col colorT) {
		paint.FillShape(gtx.Ops, col, clip.Rect(rect).Op())
	}
	fill(r.servers, colorServers)
	fill(r.channels, colorSidebar)
	fill(r.chat, colorChat)
	fill(r.side, colorSidebar)
	// The user panel, and the box to write in.
	ph := gtx.Dp(12)
	panel := image.Rect(r.channels.Min.X, r.channels.Max.Y-ph, r.channels.Max.X, r.channels.Max.Y)
	if l.UserPanel == placeTop {
		top := r.channels.Min.Y + gtx.Dp(8)
		panel = image.Rect(r.channels.Min.X, top, r.channels.Max.X, top+ph)
	}
	paint.FillShape(gtx.Ops, colorPanel, clip.Rect(panel).Op())
	in := gtx.Dp(4)
	box := image.Rect(r.chat.Min.X+in, r.chat.Max.Y-in-gtx.Dp(8), r.chat.Max.X-in, r.chat.Max.Y-in)
	paint.FillShape(gtx.Ops, colorInput, clip.UniformRRect(box, gtx.Dp(2)).Op(gtx.Ops))
	// The servers, as dots along their bar.
	dot := gtx.Dp(8)
	for i := range 4 {
		var p image.Point
		if l.serversAcross() {
			p = image.Pt(r.servers.Min.X+gtx.Dp(4)+i*(dot+gtx.Dp(4)), r.servers.Min.Y+(r.servers.Dy()-dot)/2)
		} else {
			p = image.Pt(r.servers.Min.X+(r.servers.Dx()-dot)/2, r.servers.Min.Y+gtx.Dp(4)+i*(dot+gtx.Dp(4)))
		}
		col := colorSelected
		if i == 0 {
			col = colorAccent
		}
		paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rectangle{Min: p, Max: p.Add(image.Pt(dot, dot))}).Op(gtx.Ops))
	}
	return layout.Dimensions{Size: size}
}
