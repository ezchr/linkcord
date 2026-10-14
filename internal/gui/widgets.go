package gui

import (
	"image"
	"image/color"
	"strings"
	"unicode"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// pane fills a column of the given width with bg and lays out w inside it.
func pane(gtx layout.Context, bg color.NRGBA, width int, w layout.Widget) layout.Dimensions {
	size := image.Pt(width, gtx.Constraints.Max.Y)
	gtx.Constraints = layout.Exact(size)
	paint.FillShape(gtx.Ops, bg, clip.Rect{Max: size}.Op())
	w(gtx)
	return layout.Dimensions{Size: size}
}

// vcenter lays out w at the left of the area, centered vertically. Unlike
// layout.W, it frees w from the minimum height, which would make a label as
// tall as the area with its text at the top.
func vcenter(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = 0
		return w(gtx)
	})
}

// fillMax paints the whole area available to it.
func fillMax(gtx layout.Context, bg color.NRGBA) {
	paint.FillShape(gtx.Ops, bg, clip.Rect{Max: gtx.Constraints.Max}.Op())
}

// rounded lays out w over a rounded rectangle of color bg.
func rounded(gtx layout.Context, bg color.NRGBA, radius unit.Dp, w layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			paint.FillShape(gtx.Ops, bg, clip.UniformRRect(r, gtx.Dp(radius)).Op(gtx.Ops))
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		w,
	)
}

// label returns a text label of the given size and color.
func label(th *material.Theme, size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := material.Label(th, size, tr(txt))
	l.Color = c
	return l
}

func bold(l material.LabelStyle) material.LabelStyle {
	l.Font.Weight = font.Bold
	return l
}

func italic(l material.LabelStyle) material.LabelStyle {
	l.Font.Style = font.Italic
	return l
}

func oneLine(l material.LabelStyle) material.LabelStyle {
	l.MaxLines = 1
	l.Truncator = "…"
	return l
}

// header is the title bar at the top of a column, with a line below it.
func header(gtx layout.Context, th *material.Theme, title string) layout.Dimensions {
	height := gtx.Dp(48)
	size := image.Pt(gtx.Constraints.Max.X, height)
	gtx.Constraints = layout.Exact(size)
	paint.FillShape(gtx.Ops, colorServers, clip.Rect{Min: image.Pt(0, height-1), Max: size}.Op())
	layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return vcenter(gtx, oneLine(bold(label(th, 16, colorHeader, title))).Layout)
	})
	return layout.Dimensions{Size: size}
}

// rowItem is a selectable line of a sidebar list.
// rowItem is a row of a list with its hover and selection background. A
// highlighted row, such as a conversation with unread messages, is tinted.
func rowItem(gtx layout.Context, click *widget.Clickable, w func(gtx layout.Context, hovered bool) layout.Dimensions, selected bool, highlighted ...bool) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		hovered := click.Hovered()
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				var bg color.NRGBA
				switch {
				case selected:
					bg = colorSelected
				case hovered:
					bg = colorHover
				case len(highlighted) > 0 && highlighted[0]:
					bg = withAlpha(colorAccent, 0x30)
				}
				size := image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Min.Y)
				paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(4)).Op(gtx.Ops))
				return layout.Dimensions{Size: size}
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return w(gtx, hovered)
				})
			},
		)
	})
}

// button is a filled button with a hand cursor.
func button(gtx layout.Context, th *material.Theme, click *widget.Clickable, bg color.NRGBA, txt string) layout.Dimensions {
	b := material.Button(th, click, txt)
	b.Background = bg
	b.Color = colorHeader
	b.CornerRadius = unit.Dp(4)
	b.TextSize = 14
	// Stack drops the minimum size of its children; keep it so that callers
	// can stretch the button.
	minSize := gtx.Constraints.Min
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = minSize
			return b.Layout(gtx)
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			defer clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
	)
}

// textField is an editor over a dark rounded background.
func textField(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string) layout.Dimensions {
	// Outlined, so that it shows on any colors chosen, even all black.
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions { return textFieldBody(gtx, th, ed, hint) }),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			w := max(gtx.Dp(1), 1)
			paint.FillShape(gtx.Ops, colorInput, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(4)).Path(gtx.Ops), Width: float32(w)}.Op())
			return layout.Dimensions{Size: r.Max}
		}),
	)
}

func textFieldBody(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string) layout.Dimensions {
	return rounded(gtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			e := material.Editor(th, ed, tr(hint))
			e.HintColor = colorMuted
			e.Color = colorText
			e.SelectionColor = withAlpha(colorAccent, 0x80)
			return e.Layout(gtx)
		})
	})
}

// avatar is a circle of color bg with the initials of name, the stand-in for
// a picture that is not loaded.
func avatar(gtx layout.Context, th *material.Theme, size unit.Dp, bg color.NRGBA, name string, radius int) layout.Dimensions {
	px := gtx.Dp(size)
	if radius < 0 {
		radius = px / 2
	}
	r := image.Rectangle{Max: image.Pt(px, px)}
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(r, radius).Op(gtx.Ops))
	gtx.Constraints = layout.Exact(r.Max)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := bold(label(th, unit.Sp(float32(size)*0.36), colorHeader, initials(name)))
		l.Alignment = text.Middle
		l.MaxLines = 1
		return l.Layout(gtx)
	})
	return layout.Dimensions{Size: r.Max}
}

// initials returns up to three letters that stand for name, as Discord does
// for servers without an icon.
func initials(name string) string {
	var b strings.Builder
	n := 0
	for _, word := range strings.Fields(name) {
		for _, r := range word {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
				n++
			}
			break
		}
		if n == 3 {
			break
		}
	}
	if n == 0 {
		for _, r := range name {
			return string(r)
		}
		return "?"
	}
	return b.String()
}

// colorFor returns a stable color for an id, for avatars without a picture.
func colorFor(id uint64) color.NRGBA {
	palette := []color.NRGBA{rgb(0x5865f2), rgb(0x3ba55c), rgb(0xfaa61a), rgb(0xed4245), rgb(0xeb459e), rgb(0x747f8d)}
	return palette[(id>>22)%uint64(len(palette))]
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// loader is a spinner in the accent color.
func loader(th *material.Theme) material.LoaderStyle {
	l := material.Loader(th)
	l.Color = colorAccent
	return l
}

// dot is a small filled circle, used for unread and status indicators.
func dot(gtx layout.Context, c color.NRGBA, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	r := image.Rectangle{Max: image.Pt(px, px)}
	paint.FillShape(gtx.Ops, c, clip.Ellipse(r).Op(gtx.Ops))
	return layout.Dimensions{Size: r.Max}
}
