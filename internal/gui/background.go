package gui

import (
	"bytes"
	"errors"
	"image"
	"io"
	"os"
	"slices"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"golang.org/x/image/draw"
)

// pictureSettings are the background picture: its file, how dark it is
// made under the columns and how blurred, and the colors taken from it, as
// #rrggbb by key, which the "picture" preset uses.
type pictureSettings struct {
	Path string `json:"path"`
	// Dim is how dark the picture is made, in percent.
	Dim int `json:"dim"`
	// Blur is from 0, sharp, to maxBlur.
	Blur   int               `json:"blur,omitempty"`
	Colors map[string]string `json:"colors,omitempty"`
}

const (
	maxBlur    = 10
	defaultDim = 45
	maxDim     = 95
	// pictureKey is the preset of the colors taken from the picture.
	pictureKey = "picture"
)

// The columns let the picture show through their backgrounds: the server
// list least, the chat most.
const (
	paneAlphaServers = 0.85
	paneAlphaSidebar = 0.75
	paneAlphaChat    = 0.62
)

// pictureTheme are the colors of the "picture" preset, set from the
// settings by applyAppearance.
var pictureTheme map[string]colorT

// backgroundPicture is the picture behind the columns, kept as one image no
// larger than the window, which the GPU stretches over it. Blurred, it is
// kept smaller: stretched, a small picture is a blurred one.
type backgroundPicture struct {
	img  paint.ImageOp
	size image.Point // of img; zero while there is none
	// built is what img was made for, and loading what is being made.
	built, loading backgroundKey
	busy           bool
	// seen is the window size last seen, and since when: the picture is
	// made again only once a resize has ended.
	seen   image.Point
	seenAt time.Time
	// bright is a bright color of the picture, by which it is dimmed enough
	// for text to read over it.
	bright colorT
	// failed is the file that could not be read, not tried again.
	failed string
}

type backgroundKey struct {
	path string
	blur int
	low  bool
	win  image.Point
}

// backgroundResult is a picture made for the window, and the colors taken
// from it.
type backgroundResult struct {
	img     *image.RGBA
	bright  colorT
	palette map[string]colorT
}

// resizeSettle is how long the window keeps its size before the picture is
// made again for it.
const resizeSettle = 300 * time.Millisecond

// layoutBackground draws the picture chosen over the whole area, as cover
// and centered, and darkens it. It reports whether it drew one, for the
// columns to let it show.
func (u *ui) layoutBackground(gtx layout.Context) bool {
	p := u.appearance.Picture
	b := &u.bg
	if p == nil || p.Path == "" {
		if b.size != (image.Point{}) {
			*b = backgroundPicture{}
		}
		return false
	}
	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return false
	}
	low := u.images != nil && u.images.lowPower
	want := backgroundKey{path: p.Path, blur: min(max(p.Blur, 0), maxBlur), low: low, win: size}
	now := gtx.Now
	if size != b.seen {
		b.seen, b.seenAt = size, now
	}
	if want != b.built && p.Path != b.failed && !(b.busy && b.loading == want) {
		same := want.path == b.built.path && want.blur == b.built.blur && want.low == b.built.low
		settled := now.Sub(b.seenAt) >= resizeSettle
		switch {
		case b.busy:
			// Made once what is being made is done.
			gtx.Execute(op.InvalidateCmd{At: now.Add(resizeSettle)})
		case b.size == (image.Point{}) || !same || settled:
			// A picture with no colors taken yet, as one written in the
			// settings by hand, gives its colors once read.
			u.buildBackground(want, len(p.Colors) == 0)
		default:
			gtx.Execute(op.InvalidateCmd{At: b.seenAt.Add(resizeSettle)})
		}
	}
	if b.size == (image.Point{}) {
		return false
	}
	// Cover: the picture fills the area, centered, cut at the sides that
	// are longer.
	k := max(float32(size.X)/float32(b.size.X), float32(size.Y)/float32(b.size.Y))
	off := f32.Pt((float32(size.X)-k*float32(b.size.X))/2, (float32(size.Y)-k*float32(b.size.Y))/2)
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	t := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(k, k)).Offset(off)).Push(gtx.Ops)
	b.img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	t.Pop()
	dim := pictureDim(float64(p.Dim)/100, b.bright)
	paint.FillShape(gtx.Ops, colorT{A: uint8(dim*255 + 0.5)}, clip.Rect{Max: size}.Op())
	area.Pop()
	return true
}

// pictureDim is how dark the picture is made: as the user chose, or darker
// if text would not read over its bright parts.
func pictureDim(chosen float64, bright colorT) float64 {
	text := []colorT{colorText, colorMuted, colorChannel}
	need := minPictureDim(text, colorChat, paneAlphaChat, bright)
	return min(max(chosen, need, 0), 1)
}

// minPictureDim is the least the picture must be darkened for every color
// of text to have minContrast where the picture is bright, under a column
// of surface drawn at alpha over it. The colors mix as the GPU mixes them,
// in linear light, where luminance adds up.
func minPictureDim(text []colorT, surface colorT, alpha float64, bright colorT) float64 {
	dimmest := 1.0
	for _, t := range text {
		dimmest = min(dimmest, luminance(t))
	}
	// The most luminance behind the text that keeps the contrast.
	most := (dimmest+0.05)/minContrast - 0.05
	rest := most - alpha*luminance(surface)
	lb := (1 - alpha) * luminance(bright)
	if lb <= 0 || rest >= lb {
		return 0
	}
	if rest <= 0 {
		return 1
	}
	return 1 - rest/lb
}

// buildBackground makes the picture for key in the background; derive also
// takes the colors from it and makes them the theme.
func (u *ui) buildBackground(key backgroundKey, derive bool) {
	b := &u.bg
	b.busy, b.loading = true, key
	go func() {
		res, err := makeBackground(key.path, key.win, key.blur, key.low)
		u.post(func() {
			b.busy = false
			p := u.appearance.Picture
			if p == nil || p.Path != key.path {
				return
			}
			if err != nil {
				b.failed = key.path
				if u.chat != nil {
					if derive {
						u.chat.pictureFailed()
					} else {
						u.chat.settings.look.custom.pictureErr = "Could not open the background picture."
					}
				}
				return
			}
			b.img, b.size = paint.NewImageOp(res.img), res.img.Bounds().Size()
			b.built, b.bright = key, res.bright
			if derive && u.chat != nil {
				u.chat.usePicturePalette(res.palette)
			}
			u.win.Invalidate()
		})
	}()
}

// maxPictureFile is the largest picture file read, as larger ones would
// take too much memory to decode.
const maxPictureFile = 64 << 20

// makeBackground decodes the picture at path, the first frame of an
// animation, and makes it the size of win, cut to its shape; blurred or in
// low power mode, smaller.
func makeBackground(path string, win image.Point, blur int, low bool) (backgroundResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return backgroundResult{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPictureFile+1))
	f.Close()
	if err != nil {
		return backgroundResult{}, err
	}
	if len(data) > maxPictureFile {
		return backgroundResult{}, errors.New("picture too large")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return backgroundResult{}, err
	}
	// Photos kept on their side are turned as they say.
	src = orient(src, exifOrientation(data))
	data = nil
	samples := pictureSamples(src, 48)
	res := backgroundResult{bright: brightSample(samples), palette: derivePalette(samples)}
	res.img = coverImage(src, win, backgroundShrink(blur, low))
	if blur > 0 {
		boxBlur(res.img, 2)
	}
	return res, nil
}

// backgroundShrink is how many times smaller than the window the picture
// is kept: blurred, the GPU stretching a small picture blurs it more.
func backgroundShrink(blur int, low bool) int {
	k := 1
	if blur > 0 {
		k = 1 + blur
	}
	if low {
		k *= 2
	}
	return k
}

// brightSample is the color of the samples brighter than nineteen in
// twenty of them.
func brightSample(samples []colorT) colorT {
	if len(samples) == 0 {
		return colorT{A: 0xff}
	}
	s := slices.Clone(samples)
	slices.SortStableFunc(s, func(a, b colorT) int {
		la, lb := luminance(a), luminance(b)
		switch {
		case la < lb:
			return -1
		case la > lb:
			return 1
		}
		return 0
	})
	return s[min(len(s)*19/20, len(s)-1)]
}

// coverImage cuts src to the shape of win, centered, and scales it to win
// shrunk shrink times, never larger than the part cut.
func coverImage(src image.Image, win image.Point, shrink int) *image.RGBA {
	b := src.Bounds()
	shrink = max(shrink, 1)
	// The part of src with the shape of the window.
	crop := b
	if b.Dx()*win.Y > b.Dy()*win.X {
		w := max(b.Dy()*win.X/max(win.Y, 1), 1)
		crop.Min.X += (b.Dx() - w) / 2
		crop.Max.X = crop.Min.X + w
	} else {
		h := max(b.Dx()*win.Y/max(win.X, 1), 1)
		crop.Min.Y += (b.Dy() - h) / 2
		crop.Max.Y = crop.Min.Y + h
	}
	w, h := max(win.X/shrink, 1), max(win.Y/shrink, 1)
	if w > crop.Dx() || h > crop.Dy() {
		// A small picture is kept as it is; the GPU stretches it.
		w, h = crop.Dx(), crop.Dy()
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if crop.Dx() >= 2*w && crop.Dy() >= 2*h {
		// Scaled to twice the size, then each 2 by 2 pixels averaged: the
		// quick scaler alone skips pixels when shrinking much.
		mid := image.NewRGBA(image.Rect(0, 0, 2*w, 2*h))
		draw.ApproxBiLinear.Scale(mid, mid.Bounds(), src, crop, draw.Src, nil)
		halve(dst, mid)
	} else {
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	}
	return dst
}

// halve sets each pixel of dst to the average of 2 by 2 of src, which is
// twice as large.
func halve(dst, src *image.RGBA) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	for y := range h {
		r0 := src.Pix[2*y*src.Stride:]
		r1 := src.Pix[(2*y+1)*src.Stride:]
		d := dst.Pix[y*dst.Stride:]
		for x := range w {
			for c := range 4 {
				i := 8*x + c
				d[4*x+c] = uint8((uint32(r0[i]) + uint32(r0[i+4]) + uint32(r1[i]) + uint32(r1[i+4]) + 2) / 4)
			}
		}
	}
}

// boxBlur blurs img in place: a box of radius r across, then down, three
// times, which is close to a gaussian blur.
func boxBlur(img *image.RGBA, r int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 || r <= 0 {
		return
	}
	line := make([]uint8, 4*max(w, h))
	pass := func(n, stride, step, count int) {
		for k := range count {
			base := k * stride
			// The line, read before it is written over.
			for i := range n {
				copy(line[4*i:4*i+4], img.Pix[base+i*step:base+i*step+4])
			}
			for c := range 4 {
				var sum int
				at := func(i int) int { return int(line[4*min(max(i, 0), n-1)+c]) }
				for i := -r; i <= r; i++ {
					sum += at(i)
				}
				for i := range n {
					img.Pix[base+i*step+c] = uint8(sum / (2*r + 1))
					sum += at(i+r+1) - at(i-r)
				}
			}
		}
	}
	for range 3 {
		pass(w, img.Stride, 4, h)
		pass(h, 4, img.Stride, w)
	}
}

// paneFill is the background of a column: see-through by alpha when a
// picture is behind it.
func (c *chatView) paneFill(col colorT, alpha float64) colorT {
	if !c.pictureShown {
		return col
	}
	return withAlpha(col, uint8(alpha*255+0.5))
}
