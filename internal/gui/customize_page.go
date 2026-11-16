package gui

import (
	"fmt"
	"image"
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
		path, err := zenity.SelectFile(zenity.Title("Background picture"), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"}})
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

// layoutLayoutDiagram is the window drawn small, with the bars where they
// are, in the colors of the theme and over the picture if there is one.
func (c *chatView) layoutLayoutDiagram(gtx layout.Context) layout.Dimensions {
	size := image.Pt(gtx.Dp(200), gtx.Dp(125))
	l := c.paneLayout()
	r := placePanes(l, image.Rectangle{Max: size}, gtx.Dp(14), gtx.Dp(44), gtx.Dp(38), true)
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
