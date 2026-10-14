package gui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
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
	iconOpen    = mustIcon(icons.ActionOpenInNew)
	iconZoomIn  = mustIcon(icons.ActionZoomIn)
	iconZoomOut = mustIcon(icons.ActionZoomOut)
)

const (
	maxViewerSide = 2048
	maxZoom       = 8
)

// imageViewer shows a picture over the whole window, with zoom and pan.
type imageViewer struct {
	proxy, original, openURL string
	// thumb is the URL and size of the smaller copy shown in the chat,
	// which stands in while the big one loads.
	thumb     string
	thumbSize int
	w, h      int

	// zoom is relative to the size that fits the window.
	zoom float32
	pan  f32.Point

	dragging       bool
	moved          bool
	dragFrom       f32.Point
	panFrom        f32.Point
	lastClick      time.Time
	imgRect        image.Rectangle
	closeBtn       widget.Clickable
	openBtn        widget.Clickable
	zoomIn         widget.Clickable
	zoomOut        widget.Clickable
	focusRequested bool
}

// openViewer shows a picture of w×h pixels in the viewer.
func (c *chatView) openViewer(proxy, original string, w, h int, openURL, thumb string, thumbSize int) {
	c.closeVideo()
	c.viewer = &imageViewer{
		proxy: proxy, original: original, openURL: openURL,
		thumb: thumb, thumbSize: thumbSize, w: w, h: h, zoom: 1,
	}
}

// zoomAt changes the zoom to z, keeping the point at p (relative to the
// center of the window) under the pointer.
func (v *imageViewer) zoomAt(z float32, p f32.Point) {
	z = min(max(z, 1), maxZoom)
	if z == 1 {
		v.zoom, v.pan = 1, f32.Point{}
		return
	}
	k := z / v.zoom
	v.pan = p.Sub(p.Sub(v.pan).Mul(k))
	v.zoom = z
}

func (c *chatView) layoutViewer(gtx layout.Context) layout.Dimensions {
	v := c.viewer
	return c.layoutVisualViewer(gtx, v, func(gtx layout.Context) *paint.ImageOp {
		side := min(max(v.w, v.h), maxViewerSide)
		img := c.u.images.frame(gtx, mediaURL(v.proxy, v.original, min(v.w, maxViewerSide), min(v.h, maxViewerSide)), side)
		if img == nil && v.thumb != "" {
			img = c.u.images.frame(gtx, v.thumb, v.thumbSize)
		}
		return img
	}, func() { c.viewer = nil }, nil)
}

// layoutVisualViewer gives pictures, GIFs and videos the same canvas and controls.
func (c *chatView) layoutVisualViewer(gtx layout.Context, v *imageViewer, frame func(layout.Context) *paint.ImageOp, closeViewer func(), footer layout.Widget) layout.Dimensions {
	th := c.u.th
	size := gtx.Constraints.Max
	center := layout.FPt(size).Mul(0.5)

	if v.closeBtn.Clicked(gtx) {
		closeViewer()
		return layout.Dimensions{Size: size}
	}
	// Saved to the Downloads folder, not opened in the browser.
	if v.openBtn.Clicked(gtx) {
		c.saveFile(v.openURL, "")
	}
	if v.zoomIn.Clicked(gtx) {
		v.zoomAt(v.zoom*1.5, f32.Point{})
	}
	if v.zoomOut.Clicked(gtx) {
		v.zoomAt(v.zoom/1.5, f32.Point{})
	}

	// The whole window takes the pointer: dragging pans, the wheel zooms,
	// a double click zooms in or out, a click outside the picture closes.
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, v)
	area.Pop()
	if !v.focusRequested {
		gtx.Execute(key.FocusCmd{Tag: v})
		v.focusRequested = true
	}
	for {
		ev, ok := gtx.Event(
			pointer.Filter{
				Target:  v,
				Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll,
				ScrollX: pointer.ScrollRange{Min: -1e6, Max: 1e6},
				ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6},
			},
			key.Filter{Focus: v, Name: key.NameEscape},
			key.Filter{Focus: v, Name: "+"},
			key.Filter{Focus: v, Name: "-"},
		)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case key.Event:
			if ev.State != key.Press {
				continue
			}
			switch ev.Name {
			case key.NameEscape:
				closeViewer()
				return layout.Dimensions{Size: size}
			case "+":
				v.zoomAt(v.zoom*1.5, f32.Point{})
			case "-":
				v.zoomAt(v.zoom/1.5, f32.Point{})
			}
		case pointer.Event:
			p := ev.Position.Sub(center)
			switch ev.Kind {
			case pointer.Scroll:
				v.zoomAt(v.zoom*float32(math.Pow(1.2, float64(-ev.Scroll.Y)/120)), p)
			case pointer.Press:
				if ev.Buttons != pointer.ButtonPrimary {
					continue
				}
				v.dragging, v.moved = true, false
				v.dragFrom, v.panFrom = ev.Position, v.pan
			case pointer.Drag:
				if !v.dragging {
					continue
				}
				d := ev.Position.Sub(v.dragFrom)
				if d.X*d.X+d.Y*d.Y > 16 {
					v.moved = true
				}
				if v.zoom > 1 {
					v.pan = v.panFrom.Add(d)
				}
			case pointer.Release:
				if !v.dragging {
					continue
				}
				v.dragging = false
				if v.moved {
					continue
				}
				pos := ev.Position.Round()
				if !pos.In(v.imgRect) {
					closeViewer()
					return layout.Dimensions{Size: size}
				}
				if gtx.Now.Sub(v.lastClick) < 400*time.Millisecond {
					if v.zoom > 1 {
						v.zoomAt(1, p)
					} else {
						v.zoomAt(2.5, p)
					}
					v.lastClick = time.Time{}
				} else {
					v.lastClick = gtx.Now
				}
			case pointer.Cancel:
				v.dragging = false
			}
		}
	}

	paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xe6), clip.Rect{Max: size}.Op())

	// The size that fits the window, never larger than the picture.
	margin := min(gtx.Dp(64), max(min(size.X, size.Y)/4, 0))
	w, h := float32(max(v.w, 1)), float32(max(v.h, 1))
	img := frame(gtx)
	// The picture's own shape, once loaded, rather than what Discord says,
	// which a photo turned on its side, as phones keep them, does not match:
	// drawn to that, it would be stretched.
	if img != nil {
		if s := img.Size(); s.X > 0 && s.Y > 0 {
			h = w * float32(s.Y) / float32(s.X)
		}
	}
	fit := min(float32(max(size.X-2*margin, 1))/w, float32(max(size.Y-2*margin, 1))/h, float32(gtx.Dp(1)))
	dw, dh := w*fit*v.zoom, h*fit*v.zoom
	// Keep the picture from leaving the window when panning.
	maxPanX, maxPanY := max((dw-float32(size.X))/2+float32(margin), 0), max((dh-float32(size.Y))/2+float32(margin), 0)
	v.pan.X = min(max(v.pan.X, -maxPanX), maxPanX)
	v.pan.Y = min(max(v.pan.Y, -maxPanY), maxPanY)
	pos := center.Add(v.pan).Sub(f32.Pt(dw/2, dh/2))
	v.imgRect = image.Rectangle{Min: pos.Round(), Max: pos.Add(f32.Pt(dw, dh)).Round()}

	if img != nil {
		t := op.Offset(v.imgRect.Min).Push(gtx.Ops)
		igtx := gtx
		igtx.Constraints = layout.Exact(v.imgRect.Size())
		widget.Image{Src: *img, Fit: widget.Fill}.Layout(igtx)
		t.Pop()
	}
	if img == nil && footer == nil {
		s := gtx.Dp(36)
		t := op.Offset(center.Round().Sub(image.Pt(s/2, s/2))).Push(gtx.Ops)
		lgtx := gtx
		lgtx.Constraints = layout.Exact(image.Pt(s, s))
		loader(th).Layout(lgtx)
		t.Pop()
	}

	// The buttons, in the top right corner.
	bar := func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, withAlpha(colorPopup, 0xe0), 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &v.zoomOut, iconZoomOut, colorText, colorHeader)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx,
							label(th, 13, colorText, fmt.Sprintf("%d%%", int(math.Round(float64(fit*v.zoom/float32(gtx.Dp(1))*100))))).Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &v.zoomIn, iconZoomIn, colorText, colorHeader)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						ic := iconDownload
						if d := c.downloads[v.openURL]; d != nil {
							if _, _, _, err, done := d.state(); done && err == nil {
								ic = iconDone
							}
						}
						return iconButton(gtx, &v.openBtn, ic, colorText, colorHeader)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &v.closeBtn, iconClose, colorText, colorHeader)
					}),
				)
			})
		})
	}
	macro := op.Record(gtx.Ops)
	bgtx := gtx
	bgtx.Constraints.Min = image.Point{}
	bd := bar(bgtx)
	call := macro.Stop()
	t := op.Offset(image.Pt(size.X-bd.Size.X-gtx.Dp(16), gtx.Dp(16))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()

	hint := label(th, 12, colorMuted, "Mouse wheel: zoom · Drag: pan · Double-click: zoom in · Esc: close")
	macro = op.Record(gtx.Ops)
	var hd layout.Dimensions
	if footer != nil {
		// A footer, such as a video's controls, lies over the bottom of the
		// picture, as wide as it.
		bgtx.Constraints.Max.X = max(min(v.imgRect.Dx(), size.X-gtx.Dp(32)), gtx.Dp(320))
		hd = footer(bgtx)
	} else {
		hd = hint.Layout(bgtx)
	}
	call = macro.Stop()
	footerY := size.Y - hd.Size.Y - gtx.Dp(16)
	if footer != nil {
		footerY = min(max(v.imgRect.Max.Y-hd.Size.Y, 0), footerY)
	}
	t = op.Offset(image.Pt((size.X-hd.Size.X)/2, footerY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()

	return layout.Dimensions{Size: size}
}
