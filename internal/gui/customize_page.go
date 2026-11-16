package gui

import (
	"fmt"
	"image"
	"math"
	"path/filepath"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ncruces/zenity"
)

// customizeView is the state of the Background and Layout sections of the
// Appearance page.
type customizeView struct {
	pickPicture, removePicture widget.Clickable
	dim, blur                  slider
	// pictureErr is why the picture chosen could not be used, and
	// prevPicture the one it was to replace, kept if it cannot.
	pictureErr  string
	prevPicture *pictureSettings
	// places are the buttons of the bars' places, by bar and place.
	places      map[string]*widget.Clickable
	resetLayout widget.Clickable
	// The buttons of the app's icon, and why a picture could not be it.
	pickIcon, resetIcon widget.Clickable
	iconErr             string
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

// presetList are the presets to choose, with the colors of the picture
// first when there is one.
func (c *chatView) presetList() []themePreset {
	if p := c.u.appearance.Picture; p != nil && len(p.Colors) > 0 {
		return append([]themePreset{{pictureKey, "From the Picture", nil}}, themePresets...)
	}
	return themePresets
}

// pickPicture asks for a picture to put behind the columns.
func (c *chatView) pickPicture() {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		path, err := zenity.SelectFile(zenity.Title(tr("Background picture")), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"}})
		if err != nil {
			return
		}
		c.u.post(func() { c.choosePicture(path) })
	}()
}

// choosePicture makes the picture at path the background, and its colors
// the theme once it is read.
func (c *chatView) choosePicture(path string) {
	u := c.u
	cv := &c.settings.look.custom
	prev := u.appearance.Picture
	next := &pictureSettings{Path: path, Dim: defaultDim}
	if prev != nil {
		next.Dim, next.Blur = prev.Dim, prev.Blur
	}
	cv.prevPicture, cv.pictureErr = prev, ""
	u.appearance.Picture = next
	u.bg.failed = ""
	win := u.bg.seen
	if win.X <= 0 || win.Y <= 0 {
		win = image.Pt(1280, 720)
	}
	u.buildBackground(backgroundKey{path: path, blur: next.Blur, low: u.images != nil && u.images.lowPower, win: win}, true)
}

// usePicturePalette makes the colors taken from the picture the theme,
// over which the user can change each.
func (c *chatView) usePicturePalette(pal map[string]colorT) {
	a := &c.u.appearance
	if a.Picture == nil {
		return
	}
	a.Picture.Colors = make(map[string]string, len(pal))
	for k, v := range pal {
		a.Picture.Colors[k] = hexOf(v)
	}
	a.Preset, a.Colors = pictureKey, nil
	c.settings.look.custom.prevPicture = nil
	applyAppearance(*a)
	c.themeChanged(true)
	c.refreshColorEditor()
}

// pictureFailed puts back the picture there was before the one that could
// not be read.
func (c *chatView) pictureFailed() {
	cv := &c.settings.look.custom
	cv.pictureErr = "Could not open that picture."
	c.u.appearance.Picture = cv.prevPicture
	cv.prevPicture = nil
}

// removePicture takes the background picture away, and its colors with it.
func (c *chatView) removePicture() {
	a := &c.u.appearance
	a.Picture = nil
	if a.Preset == pictureKey {
		a.Preset, a.Colors = "", nil
	}
	c.u.bg = backgroundPicture{}
	applyAppearance(*a)
	c.themeChanged(true)
	c.refreshColorEditor()
}

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
	if cv.pickPicture.Clicked(gtx) {
		c.pickPicture()
	}
	if cv.removePicture.Clicked(gtx) {
		c.removePicture()
	}
	if cv.pickIcon.Clicked(gtx) {
		c.pickIcon()
	}
	if cv.resetIcon.Clicked(gtx) {
		cv.iconErr = ""
		c.setIcon("")
	}
	if p := u.appearance.Picture; p != nil {
		if r := cv.dim.update(gtx); r.changed || r.released {
			p.Dim = int(r.value*maxDim + 0.5)
			if r.released {
				c.themeChanged(true)
			}
		}
		if r := cv.blur.update(gtx); r.changed || r.released {
			p.Blur = int(r.value*maxBlur + 0.5)
			if r.released {
				c.themeChanged(true)
			}
		}
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

// customizeRows are the Background and Layout sections of the Appearance
// page.
func (c *chatView) customizeRows() []layout.Widget {
	th := c.u.th
	cv := &c.settings.look.custom
	rows := []layout.Widget{
		settingsHeading(th, "BACKGROUND"),
		settingsText(th, "A picture behind the columns. Linkcord takes its colors and makes a theme of them, which you can change below."),
		func(gtx layout.Context) layout.Dimensions {
			p := c.u.appearance.Picture
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					txt := "Choose a Picture"
					if p != nil {
						txt = "Change Picture"
					}
					return button(gtx, th, &cv.pickPicture, colorAccent, txt)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if p == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &cv.removePicture, colorSecondaryButton, "Remove")
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if p == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, oneLine(label(th, 14, colorMuted, filepath.Base(p.Path))).Layout)
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			if cv.pictureErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, cv.pictureErr).Layout)
		},
		func(gtx layout.Context) layout.Dimensions {
			p := c.u.appearance.Picture
			if p == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, stacked(
				func(gtx layout.Context) layout.Dimensions {
					v := float32(p.Dim) / maxDim
					return c.sliderRow(gtx, &cv.dim, "Dim", v, fmt.Sprintf("%d%%", p.Dim))
				},
				func(gtx layout.Context) layout.Dimensions {
					v := float32(p.Blur) / maxBlur
					txt := "Off"
					if p.Blur > 0 {
						txt = fmt.Sprint(p.Blur)
					}
					return c.sliderRow(gtx, &cv.blur, "Blur", v, txt)
				},
				settingsText(th, "Text stays readable: a bright picture is darkened more than chosen if it has to be."),
			))
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
		settingsHeading(th, "LAYOUT"),
		settingsText(th, "Where each bar of the window goes."),
		func(gtx layout.Context) layout.Dimensions {
			var children []layout.FlexChild
			for _, b := range barChoices {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.layoutBarChoice(gtx, b)
				}))
			}
			// The window in small goes under the choices, where it has room.
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(10)}.Layout(gtx, c.layoutLayoutDiagram)
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &cv.resetLayout, colorSecondaryButton, "Reset Layout")
				})
			})
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
		settingsHeading(th, "APP ICON"),
		settingsText(th, "A picture as Linkcord's icon on the taskbar, in the notification area and on its shortcuts. The linkcord.exe file itself keeps its own icon in the folder."),
		func(gtx layout.Context) layout.Dimensions {
			chosen := c.u.appearance.Icon != ""
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					txt := "Choose an Icon"
					if chosen {
						txt = "Change Icon"
					}
					return button(gtx, th, &cv.pickIcon, colorAccent, txt)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !chosen {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &cv.resetIcon, colorSecondaryButton, "Use Linkcord's")
					})
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			if cv.iconErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, cv.iconErr).Layout)
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
	}
	return rows
}

// sliderRow is a slider with its name before it and its value after it.
func (c *chatView) sliderRow(gtx layout.Context, s *slider, name string, v float32, value string) layout.Dimensions {
	th := c.u.th
	if s.dragging {
		v = s.value
	}
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(110)
				return label(th, 14, colorText, name).Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return s.layout(gtx, 0, v) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(52)
				return layout.E.Layout(gtx, label(th, 14, colorText, value).Layout)
			}),
		)
	})
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

// hoveredBar is the bar whose place buttons the pointer is over, by its
// key, or "".
func (c *chatView) hoveredBar() string {
	cv := &c.settings.look.custom
	for _, b := range barChoices {
		for _, place := range b.places {
			if clickOf(cv.places, b.key+"/"+place).Hovered() {
				return b.key
			}
		}
	}
	return ""
}

// layoutLayoutDiagram is the window drawn small, with the bars where they
// are and what each holds, in the colors of the theme and over the picture
// if there is one. The bar whose buttons the pointer is over is outlined.
func (c *chatView) layoutLayoutDiagram(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	size := image.Pt(min(gtx.Dp(320), gtx.Constraints.Max.X), gtx.Dp(200))
	l := c.paneLayout()
	// The same placing as the window's, with the widths made small.
	r := placePanes(l, image.Rectangle{Max: size}, gtx.Dp(24), gtx.Dp(78), gtx.Dp(66), true)
	rad := gtx.Dp(6)
	defer clip.UniformRRect(image.Rectangle{Max: size}, rad).Push(gtx.Ops).Pop()
	shown := false
	if b := &c.u.bg; c.u.appearance.Picture != nil && b.size != (image.Point{}) {
		// The picture as cover, as behind the window.
		k := max(float32(size.X)/float32(b.size.X), float32(size.Y)/float32(b.size.Y))
		off := f32.Pt((float32(size.X)-k*float32(b.size.X))/2, (float32(size.Y)-k*float32(b.size.Y))/2)
		t := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(k, k)).Offset(off)).Push(gtx.Ops)
		b.img.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		t.Pop()
		dim := pictureDim(float64(c.u.appearance.Picture.Dim)/100, b.bright)
		paint.FillShape(gtx.Ops, colorT{A: uint8(dim*255 + 0.5)}, clip.Rect{Max: size}.Op())
		shown = true
	}
	fill := func(rect image.Rectangle, col colorT, alpha float64) {
		if shown {
			col = withAlpha(col, uint8(alpha*255+0.5))
		}
		paint.FillShape(gtx.Ops, col, clip.Rect(rect).Op())
	}
	fill(r.servers, colorServers, paneAlphaServers)
	fill(r.channels, colorSidebar, paneAlphaSidebar)
	fill(r.chat, colorChat, paneAlphaChat)
	fill(r.side, colorSidebar, paneAlphaSidebar)

	dp := func(v float32) int { return gtx.Dp(unit.Dp(v)) }
	// bar is a line of text drawn as a rounded bar, and dot a picture
	// drawn as a circle.
	bar := func(x, y, w, h int, col colorT) {
		if w <= 0 {
			return
		}
		paint.FillShape(gtx.Ops, col, clip.UniformRRect(image.Rect(x, y, x+w, y+h), h/2).Op(gtx.Ops))
	}
	dot := func(x, y, d int, col colorT) {
		paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rect(x, y, x+d, y+d)).Op(gtx.Ops))
	}
	// text draws a small name of a region at p, if it fits in w.
	text := func(p image.Point, w int, col colorT, txt string) layout.Dimensions {
		lgtx := gtx
		lgtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
		macro := op.Record(gtx.Ops)
		d := label(th, 9, col, txt).Layout(lgtx)
		call := macro.Stop()
		if d.Size.X > w {
			return layout.Dimensions{}
		}
		t := op.Offset(p).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return d
	}
	name, line, faint := withAlpha(colorHeader, 0xb0), withAlpha(colorMuted, 0x90), withAlpha(colorMuted, 0x50)
	in := dp(5)

	// The servers: round icons along their bar, the first one chosen.
	across := l.serversAcross()
	if s := r.servers; !s.Empty() {
		d, gap := dp(14), dp(5)
		for i := range 5 {
			col := colorSelected
			if i == 0 {
				col = colorAccent
			}
			if across {
				dot(s.Min.X+in+i*(d+gap), s.Min.Y+(s.Dy()-d)/2, d, col)
			} else {
				dot(s.Min.X+(s.Dx()-d)/2, s.Min.Y+in+i*(d+gap), d, col)
			}
		}
		if across {
			text(image.Pt(s.Min.X+in+5*(d+gap)+dp(4), s.Min.Y+dp(6)), s.Dx(), colorMuted, "Servers")
		} else {
			// Down the column the name reads turned, as there is no room across.
			lgtx := gtx
			lgtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
			macro := op.Record(gtx.Ops)
			d := label(th, 9, colorMuted, "Servers").Layout(lgtx)
			call := macro.Stop()
			at := f32.Pt(float32(s.Min.X+(s.Dx()-d.Size.Y)/2), float32(s.Max.Y-in))
			t := op.Affine(f32.Affine2D{}.Rotate(f32.Point{}, -math.Pi/2).Offset(at)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			t.Pop()
		}
	}

	// The channels: the server's name, lines with #, and the user panel.
	ph := dp(24)
	ch := r.channels
	panel := image.Rect(ch.Min.X, ch.Max.Y-ph, ch.Max.X, ch.Max.Y)
	list := image.Rect(ch.Min.X, ch.Min.Y, ch.Max.X, ch.Max.Y-ph)
	if l.UserPanel == placeTop {
		panel = image.Rect(ch.Min.X, ch.Min.Y, ch.Max.X, ch.Min.Y+ph)
		list = image.Rect(ch.Min.X, ch.Min.Y+ph, ch.Max.X, ch.Max.Y)
	}
	if !ch.Empty() {
		head := dp(18)
		bar(list.Min.X+in, list.Min.Y+dp(7), list.Dx()*6/10, dp(5), name)
		paint.FillShape(gtx.Ops, colorServers, clip.Rect(image.Rect(list.Min.X, list.Min.Y+head-1, list.Max.X, list.Min.Y+head)).Op())
		y := list.Min.Y + head + dp(3)
		y += text(image.Pt(list.Min.X+in, y), list.Dx()-in, colorMuted, "Channels").Size.Y + dp(3)
		for i := 0; y+dp(10) < list.Max.Y-dp(2) && i < 6; i++ {
			col := line
			if i == 1 {
				// The channel open, as the list marks it.
				paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rect(list.Min.X+dp(3), y-dp(1), list.Max.X-dp(3), y+dp(11)), dp(3)).Op(gtx.Ops))
				col = name
			}
			text(image.Pt(list.Min.X+in+dp(1), y-dp(1)), list.Dx(), col, "#")
			bar(list.Min.X+in+dp(10), y+dp(3), list.Dx()*(4+(i*3)%4)/10-dp(10), dp(4), col)
			y += dp(14)
		}
		paint.FillShape(gtx.Ops, colorPanel, clip.Rect(panel).Op())
		d := dp(14)
		dot(panel.Min.X+in, panel.Min.Y+(ph-d)/2, d, colorGreen)
		text(image.Pt(panel.Min.X+in+d+dp(5), panel.Min.Y+dp(6)), panel.Dx(), colorHeader, "You")
	}

	// The chat: its header, messages, and the box to write in.
	if chat := r.chat; !chat.Empty() {
		head := dp(18)
		text(image.Pt(chat.Min.X+in, chat.Min.Y+dp(3)), chat.Dx()-in, colorHeader, "Chat")
		paint.FillShape(gtx.Ops, colorServers, clip.Rect(image.Rect(chat.Min.X, chat.Min.Y+head-1, chat.Max.X, chat.Min.Y+head)).Op())
		boxH := dp(16)
		box := image.Rect(chat.Min.X+in, chat.Max.Y-in-boxH, chat.Max.X-in, chat.Max.Y-in)
		paint.FillShape(gtx.Ops, colorInput, clip.UniformRRect(box, dp(4)).Op(gtx.Ops))
		bar(box.Min.X+dp(6), box.Min.Y+(boxH-dp(4))/2, box.Dx()*4/10, dp(4), faint)
		d := dp(14)
		y := chat.Min.Y + head + dp(8)
		tw := chat.Dx() - 2*in - d - dp(6)
		for i := 0; y+dp(28) < box.Min.Y && i < 4; i++ {
			x := chat.Min.X + in
			dot(x, y, d, []colorT{colorAccent, colorYellow, colorGreen, colorRed}[i])
			x += d + dp(6)
			bar(x, y, tw*3/10, dp(5), name)
			bar(x, y+dp(9), tw*(9-(i*2)%4)/10, dp(4), line)
			bar(x, y+dp(16), tw*(4+(i*3)%5)/10, dp(4), line)
			y += dp(32)
		}
	}

	// The members: their pictures and names.
	if side := r.side; !side.Empty() {
		y := side.Min.Y + dp(5)
		y += text(image.Pt(side.Min.X+in, y), side.Dx()-in, colorMuted, "Members").Size.Y + dp(5)
		d := dp(12)
		for i := 0; y+d < side.Max.Y-dp(2) && i < 7; i++ {
			col := colorGreen
			if i > 3 {
				col = colorSelected
			}
			dot(side.Min.X+in, y, d, col)
			bar(side.Min.X+in+d+dp(5), y+dp(4), (side.Dx()-2*in-d-dp(5))*(6+(i*3)%4)/10, dp(4), line)
			y += dp(18)
		}
	}

	// The bar whose buttons are under the pointer, outlined.
	var mark image.Rectangle
	switch c.hoveredBar() {
	case "servers":
		mark = r.servers
	case "channels":
		mark = r.channels
	case "members":
		mark = r.side
	case "panel":
		mark = panel
	}
	if !mark.Empty() {
		paint.FillShape(gtx.Ops, colorAccent, clip.Stroke{Path: clip.Rect(mark.Inset(1)).Path(), Width: float32(dp(2))}.Op())
	}
	return layout.Dimensions{Size: size}
}
