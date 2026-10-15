package gui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

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
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// isImageFile reports whether the file at path is a picture the editor
// opens, by its name.
func isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// loadThumb decodes the picture of f small, for its preview above the box.
func (c *chatView) loadThumb(f *pendingFile) {
	if f.thumbLoading || f.thumb != nil {
		return
	}
	f.thumbLoading = true
	path := f.path
	go func() {
		img, err := decodeFile(path)
		c.u.post(func() {
			f.thumbLoading = false
			if err != nil || f.path != path {
				return
			}
			op := paint.NewImageOp(scale(img, 240))
			f.thumb = &op
		})
	}()
}

func decodeFile(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	return img, err
}

// layoutImageChip is the preview of a picture to send, which opens the
// editor when clicked, with its button to remove it.
func (c *chatView) layoutImageChip(gtx layout.Context, f *pendingFile) layout.Dimensions {
	c.loadThumb(f)
	if f.edit.Clicked(gtx) {
		c.openImageEditor(f)
	}
	size := gtx.Dp(140)
	return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Stack{Alignment: layout.NE}.Layout(gtx,
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return f.edit.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								sz := image.Pt(size, size)
								defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(4)).Push(gtx.Ops).Pop()
								paint.FillShape(gtx.Ops, colorServers, clip.Rect{Max: sz}.Op())
								if f.thumb != nil {
									igtx := gtx
									igtx.Constraints = layout.Exact(sz)
									widget.Image{Src: *f.thumb, Fit: widget.Contain, Position: layout.Center}.Layout(igtx)
								}
								if f.edit.Hovered() {
									paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x70), clip.Rect{Max: sz}.Op())
									t := op.Offset(image.Pt((size-gtx.Dp(24))/2, (size-gtx.Dp(24))/2)).Push(gtx.Ops)
									icon(gtx, iconEdit, 24, colorHeader)
									t.Pop()
								}
								return layout.Dimensions{Size: sz}
							})
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							// On a dark round patch, to show on any picture.
							return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return rounded(gtx, withAlpha(rgb(0x000000), 0xa0), 16, func(gtx layout.Context) layout.Dimensions {
									return iconButton(gtx, &f.remove, iconClose, colorHeader, colorRed)
								})
							})
						}))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = size
					return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, oneLine(label(c.u.th, 12, colorText, f.name)).Layout)
				}),
			)
		})
	})
}

// imageEditor edits a picture before it is sent: crop, draw, rotate and
// flip, with undo. Save writes the result in place of the attachment.
type imageEditor struct {
	file *pendingFile
	img  *image.NRGBA
	op   paint.ImageOp
	undo []*image.NRGBA
	err  string

	tool int
	// pen is the color drawn with, from penColors.
	pen int
	// stroke is the line being drawn, in the picture's pixels; sel the box
	// being cropped to, from its corner a to b.
	stroke    []f32.Point
	selA      f32.Point
	selB      f32.Point
	selecting bool
	hasSel    bool
	// cropDrag is what a drag in crop mode does: a corner it moves (0 to
	// 3), cropMove to move the box, or cropNew for a new box; grab is
	// where it started.
	cropDrag int
	grab     f32.Point
	grabA    f32.Point
	grabB    f32.Point
	// imgRect is where the picture is drawn, and its scale.
	imgRect image.Rectangle
	k       float32

	toolBtns                    [2]widget.Clickable
	penBtns                     [6]widget.Clickable
	rotate, flip, undoBtn, crop widget.Clickable
	save, cancel, reset         widget.Clickable
	focused                     bool
}

const (
	toolDraw = iota
	toolCrop
)

const (
	cropMove = 4 + iota
	cropNew
)

var penColors = []colorT{rgb(0xffffff), rgb(0x000000), rgb(0xf23f43), rgb(0xf0b232), rgb(0x23a55a), rgb(0x5865f2)}

// maxEditSide bounds the pictures edited, against huge photos.
const maxEditSide = 4096

func (c *chatView) openImageEditor(f *pendingFile) {
	img, err := decodeFile(f.path)
	if err != nil {
		c.sendErr = "Could not open the picture: " + err.Error()
		return
	}
	if b := img.Bounds(); b.Dx() > maxEditSide || b.Dy() > maxEditSide {
		img = scale(img, maxEditSide)
	}
	e := &imageEditor{file: f}
	e.set(toNRGBA(img))
	c.imgEdit = e
}

func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

// set makes img the picture edited, keeping the one before for undo.
func (e *imageEditor) set(img *image.NRGBA) {
	if e.img != nil {
		e.undo = append(e.undo, e.img)
		if len(e.undo) > 20 {
			e.undo = e.undo[1:]
		}
	}
	e.img = img
	e.op = paint.NewImageOp(img)
	e.frameAll()
}

func rotateRight(src *image.NRGBA) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.SetNRGBA(b.Dy()-1-y, x, src.NRGBAAt(x, y))
		}
	}
	return out
}

func flipHorizontal(src *image.NRGBA) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(b)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.SetNRGBA(b.Dx()-1-x, y, src.NRGBAAt(x, y))
		}
	}
	return out
}

func cropTo(src *image.NRGBA, r image.Rectangle) *image.NRGBA {
	r = r.Intersect(src.Bounds())
	if r.Empty() {
		return src
	}
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
	return out
}

// drawStroke paints a line through pts, w pixels wide, onto a copy of src.
func drawStroke(src *image.NRGBA, pts []f32.Point, w float32, col color.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(src.Bounds())
	copy(out.Pix, src.Pix)
	r := w / 2
	disc := func(cx, cy float32) {
		for y := int(cy - r); y <= int(cy+r); y++ {
			for x := int(cx - r); x <= int(cx+r); x++ {
				dx, dy := float32(x)+0.5-cx, float32(y)+0.5-cy
				if dx*dx+dy*dy <= r*r && image.Pt(x, y).In(out.Bounds()) {
					out.SetNRGBA(x, y, col)
				}
			}
		}
	}
	for i, p := range pts {
		if i == 0 {
			disc(p.X, p.Y)
			continue
		}
		q := pts[i-1]
		d := float32(math.Hypot(float64(p.X-q.X), float64(p.Y-q.Y)))
		steps := int(d/(r/2)) + 1
		for s := 1; s <= steps; s++ {
			t := float32(s) / float32(steps)
			disc(q.X+(p.X-q.X)*t, q.Y+(p.Y-q.Y)*t)
		}
	}
	return out
}

// toImage is the point p of the window in the picture's pixels.
func (e *imageEditor) toImage(p f32.Point) f32.Point {
	return f32.Pt((p.X-float32(e.imgRect.Min.X))/e.k, (p.Y-float32(e.imgRect.Min.Y))/e.k)
}

func (e *imageEditor) penWidth() float32 {
	b := e.img.Bounds()
	return max(3, float32(max(b.Dx(), b.Dy()))/150)
}

func (c *chatView) closeImageEditor() {
	c.imgEdit = nil
	gtxFocus(c)
}

// saveImageEditor writes the picture as a PNG beside the pasted ones, in
// place of the attachment.
func (c *chatView) saveImageEditor() {
	e := c.imgEdit
	if err := os.MkdirAll(pasteDir, 0o700); err != nil {
		e.err = err.Error()
		return
	}
	out, err := os.CreateTemp(pasteDir, "edited-*.png")
	if err != nil {
		e.err = err.Error()
		return
	}
	err = png.Encode(out, e.img)
	out.Close()
	info, serr := os.Stat(out.Name())
	if err != nil || serr != nil {
		e.err = "Could not save the picture"
		return
	}
	if limit := c.uploadLimit(); info.Size() > limit {
		e.err = fmt.Sprintf(tr("The picture is %s; you can send files up to %s here."), formatSize(uint64(info.Size())), formatSize(uint64(limit)))
		return
	}
	f := e.file
	f.path, f.size = out.Name(), info.Size()
	f.name = strings.TrimSuffix(f.name, filepath.Ext(f.name)) + ".png"
	f.thumb = nil
	c.closeImageEditor()
}

func (c *chatView) layoutImageEditor(gtx layout.Context) {
	e := c.imgEdit
	th := c.u.th
	size := gtx.Constraints.Max
	if !e.focused {
		gtx.Execute(key.FocusCmd{Tag: e})
		e.focused = true
	}
	// The buttons.
	for i := range e.toolBtns {
		if e.toolBtns[i].Clicked(gtx) && e.tool != i {
			e.tool = i
			e.frameAll()
		}
	}
	for i := range e.penBtns {
		if e.penBtns[i].Clicked(gtx) {
			e.pen, e.tool = i, toolDraw
		}
	}
	if e.rotate.Clicked(gtx) {
		e.set(rotateRight(e.img))
	}
	if e.flip.Clicked(gtx) {
		e.set(flipHorizontal(e.img))
	}
	if e.undoBtn.Clicked(gtx) {
		e.undoLast()
	}
	if e.reset.Clicked(gtx) && len(e.undo) > 0 {
		e.set(e.undo[0])
		e.undo = nil
	}
	if e.crop.Clicked(gtx) {
		e.applyCrop()
	}
	if e.cancel.Clicked(gtx) {
		c.closeImageEditor()
		return
	}
	if e.save.Clicked(gtx) {
		// A crop framed but not applied is what is meant.
		e.applyCrop()
		c.saveImageEditor()
		if c.imgEdit == nil {
			return
		}
	}
	for {
		// Whatever has the focus, as the editor covers everything.
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameReturn}, key.Filter{Name: key.NameEnter}, key.Filter{Name: "Z", Required: key.ModShortcut})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			if ke.Name == key.NameEscape {
				c.closeImageEditor()
				return
			}
			if ke.Name == key.NameReturn || ke.Name == key.NameEnter {
				e.applyCrop()
				continue
			}
			e.undoLast()
		}
	}
	// Drawing and cropping, on the picture.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &e.imgRect, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		p := e.toImage(pe.Position.Add(layout.FPt(e.imgRect.Min)))
		switch pe.Kind {
		case pointer.Press:
			if e.tool == toolDraw {
				e.stroke = []f32.Point{p}
			} else {
				e.startCropDrag(p, float32(gtx.Dp(16))/e.k)
			}
		case pointer.Drag:
			if e.tool == toolDraw && e.stroke != nil {
				e.stroke = append(e.stroke, p)
			} else if e.selecting {
				e.moveCropDrag(p)
			}
		case pointer.Release, pointer.Cancel:
			if e.tool == toolDraw && len(e.stroke) > 0 {
				e.set(drawStroke(e.img, e.stroke, e.penWidth(), penColors[e.pen]))
				e.stroke = nil
			} else if e.selecting {
				e.moveCropDrag(p)
				e.selecting = false
				e.normalize()
				e.hasSel = e.selB.X-e.selA.X > 2 && e.selB.Y-e.selA.Y > 2
				if !e.hasSel {
					e.frameAll()
				}
			}
		}
	}

	paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0xe6), clip.Rect{Max: size}.Op())
	// Clicks stay in the editor.
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, e)
	area.Pop()

	barH := gtx.Dp(64)
	margin := gtx.Dp(24)
	b := e.img.Bounds()
	avail := image.Pt(size.X-2*margin, size.Y-barH-2*margin)
	e.k = min(float32(avail.X)/float32(b.Dx()), float32(avail.Y)/float32(b.Dy()), float32(gtx.Dp(1)))
	dw, dh := int(float32(b.Dx())*e.k), int(float32(b.Dy())*e.k)
	e.imgRect = image.Rectangle{Min: image.Pt((size.X-dw)/2, margin+(avail.Y-dh)/2)}
	e.imgRect.Max = e.imgRect.Min.Add(image.Pt(dw, dh))

	t := op.Offset(e.imgRect.Min).Push(gtx.Ops)
	igtx := gtx
	igtx.Constraints = layout.Exact(image.Pt(dw, dh))
	widget.Image{Src: e.op, Fit: widget.Fill}.Layout(igtx)
	// The line being drawn, and the box being cropped to.
	toWin := func(p f32.Point) f32.Point { return p.Mul(e.k) }
	if len(e.stroke) > 0 {
		var path clip.Path
		path.Begin(gtx.Ops)
		path.MoveTo(toWin(e.stroke[0]))
		for _, p := range e.stroke[1:] {
			path.LineTo(toWin(p))
		}
		if len(e.stroke) == 1 {
			path.LineTo(toWin(e.stroke[0]).Add(f32.Pt(0.1, 0)))
		}
		paint.FillShape(gtx.Ops, penColors[e.pen], clip.Stroke{Path: path.End(), Width: e.penWidth() * e.k}.Op())
	}
	if e.selecting || e.hasSel {
		a, bb := toWin(e.selA), toWin(e.selB)
		r := image.Rect(int(min(a.X, bb.X)), int(min(a.Y, bb.Y)), int(max(a.X, bb.X)), int(max(a.Y, bb.Y)))
		// Dark outside the box, as croppers show it.
		for _, o := range []image.Rectangle{
			{Max: image.Pt(dw, r.Min.Y)}, {Min: image.Pt(0, r.Max.Y), Max: image.Pt(dw, dh)},
			{Min: image.Pt(0, r.Min.Y), Max: image.Pt(r.Min.X, r.Max.Y)}, {Min: image.Pt(r.Max.X, r.Min.Y), Max: image.Pt(dw, r.Max.Y)},
		} {
			paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x90), clip.Rect(o.Intersect(image.Rect(0, 0, dw, dh))).Op())
		}
		paint.FillShape(gtx.Ops, colorHeader, clip.Stroke{Path: clip.Rect(r).Path(), Width: float32(gtx.Dp(2))}.Op())
		// Thirds, and a handle at each corner, to drag.
		for i := 1; i < 3; i++ {
			x, y := r.Min.X+r.Dx()*i/3, r.Min.Y+r.Dy()*i/3
			paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x60), clip.Rect{Min: image.Pt(x, r.Min.Y), Max: image.Pt(x+1, r.Max.Y)}.Op())
			paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x60), clip.Rect{Min: image.Pt(r.Min.X, y), Max: image.Pt(r.Max.X, y+1)}.Op())
		}
		h := gtx.Dp(6)
		for _, pt := range []image.Point{r.Min, {r.Max.X, r.Min.Y}, {r.Min.X, r.Max.Y}, r.Max} {
			paint.FillShape(gtx.Ops, colorHeader, clip.Rect{Min: pt.Sub(image.Pt(h, h)), Max: pt.Add(image.Pt(h, h))}.Op())
		}
	}
	area = clip.Rect{Max: image.Pt(dw, dh)}.Push(gtx.Ops)
	if e.tool == toolDraw {
		pointer.CursorCrosshair.Add(gtx.Ops)
	} else {
		pointer.CursorCrosshair.Add(gtx.Ops)
	}
	event.Op(gtx.Ops, &e.imgRect)
	area.Pop()
	t.Pop()

	// The bar of tools at the bottom.
	btn := func(cl *widget.Clickable, text string, on bool, bg colorT) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					col := bg
					if on {
						col = colorSelected
					}
					if cl.Hovered() {
						col = withAlpha(col, 0xd0)
					}
					return rounded(gtx, col, 6, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(7), Bottom: unit.Dp(7)}.Layout(gtx,
							bold(label(th, 13, colorHeader, text)).Layout)
					})
				})
			})
		})
	}
	var items []layout.FlexChild
	items = append(items,
		btn(&e.toolBtns[toolDraw], "Draw", e.tool == toolDraw, colorMenu),
		btn(&e.toolBtns[toolCrop], "Crop", e.tool == toolCrop, colorMenu))
	if e.tool == toolDraw {
		for i := range penColors {
			items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return e.penBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					s := gtx.Dp(26)
					if e.pen == i {
						paint.FillShape(gtx.Ops, colorHeader, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
					}
					in := gtx.Dp(3)
					paint.FillShape(gtx.Ops, penColors[i], clip.Ellipse(image.Rect(in, in, s-in, s-in)).Op(gtx.Ops))
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: image.Pt(s, s)}
					})
				})
			}))
		}
	} else {
		items = append(items, btn(&e.crop, "Apply Crop", false, colorAccent))
	}
	items = append(items,
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		btn(&e.rotate, "Rotate", false, colorMenu),
		btn(&e.flip, "Flip", false, colorMenu),
		btn(&e.undoBtn, "Undo", false, colorMenu),
		btn(&e.reset, "Reset", false, colorMenu),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		btn(&e.cancel, "Cancel", false, colorMenu),
		btn(&e.save, "Save", false, colorGreenButton))
	macro := op.Record(gtx.Ops)
	bgtx := gtx
	bgtx.Constraints.Min = image.Point{}
	bd := layout.Flex{Alignment: layout.Middle}.Layout(bgtx, items...)
	call := macro.Stop()
	t = op.Offset(image.Pt((size.X-bd.Size.X)/2, size.Y-barH+(barH-bd.Size.Y)/2-gtx.Dp(8))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	if e.err != "" {
		t = op.Offset(image.Pt(margin, size.Y-barH-gtx.Dp(20))).Push(gtx.Ops)
		label(th, 13, colorRed, e.err).Layout(gtx)
		t.Pop()
	}
}

// frameAll frames the whole picture for cropping.
func (e *imageEditor) frameAll() {
	b := e.img.Bounds()
	e.selA, e.selB = f32.Pt(0, 0), f32.Pt(float32(b.Dx()), float32(b.Dy()))
	e.hasSel, e.selecting = e.tool == toolCrop, false
}

// normalize puts selA at the top left of the box and selB at the bottom
// right, inside the picture.
func (e *imageEditor) normalize() {
	b := e.img.Bounds()
	clamp := func(p f32.Point) f32.Point {
		return f32.Pt(min(max(p.X, 0), float32(b.Dx())), min(max(p.Y, 0), float32(b.Dy())))
	}
	a, c := clamp(e.selA), clamp(e.selB)
	e.selA, e.selB = f32.Pt(min(a.X, c.X), min(a.Y, c.Y)), f32.Pt(max(a.X, c.X), max(a.Y, c.Y))
}

// startCropDrag starts a drag at p, in the picture's pixels: on a corner
// of the box, near enough, it moves that corner; inside, the whole box;
// elsewhere it draws a new one.
func (e *imageEditor) startCropDrag(p f32.Point, near float32) {
	e.selecting, e.grab, e.grabA, e.grabB = true, p, e.selA, e.selB
	if e.hasSel {
		corners := []f32.Point{e.selA, {X: e.selB.X, Y: e.selA.Y}, {X: e.selA.X, Y: e.selB.Y}, e.selB}
		for i, c := range corners {
			if math.Abs(float64(p.X-c.X)) <= float64(near) && math.Abs(float64(p.Y-c.Y)) <= float64(near) {
				e.cropDrag = i
				return
			}
		}
		if p.X > e.selA.X && p.X < e.selB.X && p.Y > e.selA.Y && p.Y < e.selB.Y {
			e.cropDrag = cropMove
			return
		}
	}
	e.cropDrag = cropNew
	e.selA, e.selB, e.hasSel = p, p, true
}

func (e *imageEditor) moveCropDrag(p f32.Point) {
	switch e.cropDrag {
	case 0:
		e.selA = p
	case 1:
		e.selB.X, e.selA.Y = p.X, p.Y
	case 2:
		e.selA.X, e.selB.Y = p.X, p.Y
	case 3:
		e.selB = p
	case cropMove:
		// Moved whole, kept inside the picture.
		b := e.img.Bounds()
		d := p.Sub(e.grab)
		w, h := e.grabB.X-e.grabA.X, e.grabB.Y-e.grabA.Y
		x := min(max(e.grabA.X+d.X, 0), float32(b.Dx())-w)
		y := min(max(e.grabA.Y+d.Y, 0), float32(b.Dy())-h)
		e.selA, e.selB = f32.Pt(x, y), f32.Pt(x+w, y+h)
	default:
		e.selB = p
	}
}

// applyCrop crops to the box, if it frames less than the whole picture.
func (e *imageEditor) applyCrop() {
	if e.tool != toolCrop || !e.hasSel || e.selecting {
		return
	}
	e.normalize()
	r := image.Rect(int(e.selA.X+0.5), int(e.selA.Y+0.5), int(e.selB.X+0.5), int(e.selB.Y+0.5))
	if r.Dx() > 2 && r.Dy() > 2 && r != e.img.Bounds() {
		e.set(cropTo(e.img, r))
	}
	e.frameAll()
}

// undoLast takes back the last change, by the button or Ctrl+Z.
func (e *imageEditor) undoLast() {
	if len(e.undo) == 0 {
		return
	}
	e.img = e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.op = paint.NewImageOp(e.img)
	e.frameAll()
}
