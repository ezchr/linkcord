package gui

import (
	"fmt"
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// appearanceView is the state of the Appearance page of the settings.
type appearanceView struct {
	presets, colors map[string]*widget.Clickable
	// editing is the color whose editor is open, with its red, green and
	// blue and its hexadecimal code.
	editing  string
	rgb      [3]slider
	hex      widget.Editor
	resetOne widget.Clickable
	resetAll widget.Clickable
}

// themeColorOf returns the color of the interface named key.
func themeColorOf(key string) *themeColor {
	for _, g := range themeGroups {
		for i := range g.colors {
			if g.colors[i].key == key {
				return &g.colors[i]
			}
		}
	}
	return nil
}

// themeChanged has what keeps its own copy of the colors follow them, and
// keeps them unless in the demo.
func (c *chatView) themeChanged(save bool) {
	u := c.u
	u.th.Palette = material.Palette{Fg: colorText, Bg: colorChat, ContrastBg: colorAccent, ContrastFg: colorHeader}
	if save && !c.demo {
		saveAppearance(u.appearance)
	}
}

// setThemeColor changes the color key to v, over the preset.
func (c *chatView) setThemeColor(key string, v colorT, save bool) {
	tc := themeColorOf(key)
	if tc == nil {
		return
	}
	*tc.v = v
	a := &c.u.appearance
	if a.Colors == nil {
		a.Colors = make(map[string]string)
	}
	if v == presetColor(a.Preset, key) {
		delete(a.Colors, key)
	} else {
		a.Colors[key] = hexOf(v)
	}
	c.themeChanged(save)
}

func (c *chatView) updateAppearance(gtx layout.Context) {
	u := c.u
	av := &c.settings.look
	if av.presets == nil {
		av.presets = make(map[string]*widget.Clickable)
		av.colors = make(map[string]*widget.Clickable)
		av.hex.SingleLine, av.hex.Submit = true, true
	}
	for _, p := range themePresets {
		if clickOf(av.presets, p.key).Clicked(gtx) {
			u.appearance = appearanceSettings{Preset: p.key}
			applyAppearance(u.appearance)
			c.themeChanged(true)
			if tc := themeColorOf(av.editing); tc != nil {
				av.hex.SetText(hexOf(*tc.v))
			}
		}
	}
	for _, g := range themeGroups {
		for _, tc := range g.colors {
			if !clickOf(av.colors, tc.key).Clicked(gtx) {
				continue
			}
			if av.editing == tc.key {
				av.editing = ""
			} else {
				av.editing = tc.key
				av.hex.SetText(hexOf(*tc.v))
			}
		}
	}
	if av.resetAll.Clicked(gtx) {
		u.appearance.Colors = nil
		applyAppearance(u.appearance)
		c.themeChanged(true)
		if tc := themeColorOf(av.editing); tc != nil {
			av.hex.SetText(hexOf(*tc.v))
		}
	}
	tc := themeColorOf(av.editing)
	if tc == nil {
		return
	}
	if av.resetOne.Clicked(gtx) {
		c.setThemeColor(tc.key, presetColor(u.appearance.Preset, tc.key), true)
		av.hex.SetText(hexOf(*tc.v))
	}
	for i := range av.rgb {
		r := av.rgb[i].update(gtx)
		if !r.changed && !r.released {
			continue
		}
		v := *tc.v
		n := uint8(min(max(r.value, 0), 1)*255 + 0.5)
		switch i {
		case 0:
			v.R = n
		case 1:
			v.G = n
		case 2:
			v.B = n
		}
		c.setThemeColor(tc.key, v, r.released)
		av.hex.SetText(hexOf(v))
	}
	for {
		ev, ok := av.hex.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.ChangeEvent, widget.SubmitEvent:
			if v, ok := parseHex(av.hex.Text()); ok && v != *tc.v {
				c.setThemeColor(tc.key, v, true)
			}
		}
	}
}

// appearanceRows are the Appearance page: presets, then every color.
func (c *chatView) appearanceRows() []layout.Widget {
	u := c.u
	th := u.th
	av := &c.settings.look
	if av.presets == nil {
		return nil
	}
	rows := []layout.Widget{
		settingsHeading(th, "PRESETS"),
		settingsText(th, "A preset sets every color at once; the colors below change on top of it."),
	}
	for i := 0; i < len(themePresets); i += 3 {
		group := themePresets[i:min(i+3, len(themePresets))]
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, 2*len(group))
			for j, p := range group {
				if j > 0 {
					children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout))
				}
				children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return c.layoutPresetCard(gtx, p)
				}))
			}
			for j := len(group); j < 3; j++ {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout), layout.Flexed(1, layout.Spacer{}.Layout))
			}
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx, children...)
			})
		})
	}
	for _, g := range themeGroups {
		rows = append(rows,
			func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
			settingsHeading(th, g.title),
		)
		for _, tc := range g.colors {
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return c.layoutThemeColor(gtx, tc)
			})
		}
	}
	return append(rows,
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 16) },
		func(gtx layout.Context) layout.Dimensions {
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, &av.resetAll, colorSecondaryButton, "Reset All Colors to the Preset")
			})
		},
	)
}

// layoutPresetCard is a preset to choose: its colors in a strip, its name,
// and an outline when chosen.
func (c *chatView) layoutPresetCard(gtx layout.Context, p themePreset) layout.Dimensions {
	th := c.u.th
	av := &c.settings.look
	cl := clickOf(av.presets, p.key)
	chosen := c.u.appearance.Preset == p.key || c.u.appearance.Preset == "" && p.key == "dark"
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		w := gtx.Constraints.Max.X
		h := gtx.Dp(76)
		r := gtx.Dp(8)
		bg := presetColor(p.key, "chat")
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rect(0, 0, w, h), r).Op(gtx.Ops))
		// A strip of its colors at the top, as the window would look.
		strip := []string{"servers", "sidebar", "chat", "accent", "text"}
		sw := w / len(strip)
		for i, key := range strip {
			x := i * sw
			if i == len(strip)-1 {
				sw = w - x
			}
			rect := image.Rect(x, 0, x+sw, gtx.Dp(36))
			st := clip.UniformRRect(image.Rect(0, 0, w, h), r).Push(gtx.Ops)
			paint.FillShape(gtx.Ops, presetColor(p.key, key), clip.Rect(rect).Op())
			st.Pop()
		}
		outline, width := presetColor(p.key, "input"), max(gtx.Dp(1), 1)
		if chosen {
			outline, width = colorAccent, gtx.Dp(2)
		} else if cl.Hovered() {
			outline = colorMuted
		}
		paint.FillShape(gtx.Ops, outline, clip.Stroke{Path: clip.UniformRRect(image.Rect(0, 0, w, h), r).Path(gtx.Ops), Width: float32(width)}.Op())
		lgtx := gtx
		lgtx.Constraints = layout.Exact(image.Pt(w, h-gtx.Dp(36)))
		layoutAt(lgtx, image.Pt(0, gtx.Dp(36)), func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, oneLine(bold(label(th, 14, presetColor(p.key, "header"), p.name))).Layout)
		})
		return layout.Dimensions{Size: image.Pt(w, h)}
	})
}

// layoutThemeColor is a color of the interface: a swatch, its name and what
// it colors, and its code; clicked, its editor opens under it.
func (c *chatView) layoutThemeColor(gtx layout.Context, tc themeColor) layout.Dimensions {
	th := c.u.th
	av := &c.settings.look
	open := av.editing == tc.key
	changed := false
	if _, ok := c.u.appearance.Colors[tc.key]; ok {
		changed = true
	}
	row := func(gtx layout.Context) layout.Dimensions {
		return clickOf(av.colors, tc.key).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return swatch(gtx, *tc.v, 32) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					layout.Flexed(1, stacked(
						label(th, 15, colorHeader, tc.name).Layout,
						label(th, 13, colorMuted, tc.about).Layout,
					)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						txt := hexOf(*tc.v)
						if changed {
							txt += " •"
						}
						return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8)}.Layout(gtx, label(th, 13, colorMuted, txt).Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						ic := iconFold
						if open {
							ic = iconExpand
						}
						return icon(gtx, ic, 18, colorMuted)
					}),
				)
			})
		})
	}
	if !open {
		return row(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(row),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(44), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return c.layoutColorEditor(gtx, tc)
					})
				})
			})
		}),
	)
}

// layoutColorEditor is the red, green and blue of the color being edited,
// its code to type, and its reset.
func (c *chatView) layoutColorEditor(gtx layout.Context, tc themeColor) layout.Dimensions {
	th := c.u.th
	av := &c.settings.look
	v := *tc.v
	parts := []struct {
		name string
		n    uint8
		col  colorT
	}{{"Red", v.R, rgb(0xed4245)}, {"Green", v.G, rgb(0x3ba55c)}, {"Blue", v.B, rgb(0x5865f2)}}
	children := []layout.FlexChild{}
	for i, p := range parts {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				n := p.n
				if av.rgb[i].dragging {
					n = uint8(min(max(av.rgb[i].value, 0), 1)*255 + 0.5)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(56)
						return label(th, 14, p.col, p.name).Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return av.rgb[i].layout(gtx, 0, float32(n)/255)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(40)
						return layout.E.Layout(gtx, label(th, 14, colorText, fmt.Sprint(n)).Layout)
					}),
				)
			})
		}))
	}
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(56)
					return label(th, 14, colorMuted, "Code").Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = gtx.Dp(140)
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return textField(gtx, th, &av.hex, "#rrggbb")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &av.resetOne, colorSecondaryButton, "Reset")
				}),
			)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// swatch is a square of color c, size wide, outlined so that it shows on
// any background.
func swatch(gtx layout.Context, c colorT, size unit.Dp) layout.Dimensions {
	s := gtx.Dp(size)
	r := image.Rect(0, 0, s, s)
	paint.FillShape(gtx.Ops, c, clip.UniformRRect(r, gtx.Dp(6)).Op(gtx.Ops))
	w := max(gtx.Dp(1), 1)
	paint.FillShape(gtx.Ops, withAlpha(colorMuted, 0x80), clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(6)).Path(gtx.Ops), Width: float32(w)}.Op())
	return layout.Dimensions{Size: r.Max}
}

// layoutAt lays out w at at.
func layoutAt(gtx layout.Context, at image.Point, w layout.Widget) layout.Dimensions {
	defer op.Offset(at).Push(gtx.Ops).Pop()
	return w(gtx)
}
