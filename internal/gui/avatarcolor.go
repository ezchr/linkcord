package gui

import (
	"image"
	"image/color"
	"sort"
)

// Discord colors the voice tile of someone without video with the primary
// color of their avatar (the VideoBackground component: the first color of
// the avatar's palette, PRIMARY_800 while it loads). The palette comes from
// color-thief's modified median cut quantization (MMCQ): 5 colors, every
// 10th pixel, skipping pixels mostly transparent (alpha < 125) or nearly white
// (all channels > 250). An avatar with no such pixel gives black.
//
// This is a port of that quantization, so the colors match.

const (
	mmcqSigBits   = 5
	mmcqRShift    = 8 - mmcqSigBits
	mmcqMaxIter   = 1000
	mmcqFractPop  = 0.75
	paletteColors = 5
	paletteStep   = 10
)

func mmcqIndex(r, g, b int) int { return r<<(2*mmcqSigBits) | g<<mmcqSigBits | b }

type vbox struct {
	r1, r2, g1, g2, b1, b2 int
	histo                  []int
	count                  int
	countSet               bool
	avg                    [3]int
	avgSet                 bool
}

func (v *vbox) volume() int {
	return (v.r2 - v.r1 + 1) * (v.g2 - v.g1 + 1) * (v.b2 - v.b1 + 1)
}

func (v *vbox) cnt() int {
	if !v.countSet {
		n := 0
		for r := v.r1; r <= v.r2; r++ {
			for g := v.g1; g <= v.g2; g++ {
				for b := v.b1; b <= v.b2; b++ {
					n += v.histo[mmcqIndex(r, g, b)]
				}
			}
		}
		v.count, v.countSet = n, true
	}
	return v.count
}

func (v *vbox) copy() *vbox {
	return &vbox{r1: v.r1, r2: v.r2, g1: v.g1, g2: v.g2, b1: v.b1, b2: v.b2, histo: v.histo}
}

func (v *vbox) average() [3]int {
	if v.avgSet {
		return v.avg
	}
	const mult = 1 << mmcqRShift
	var tot int
	var rs, gs, bs float64
	for r := v.r1; r <= v.r2; r++ {
		for g := v.g1; g <= v.g2; g++ {
			for b := v.b1; b <= v.b2; b++ {
				h := v.histo[mmcqIndex(r, g, b)]
				tot += h
				rs += float64(h) * (float64(r) + 0.5) * mult
				gs += float64(h) * (float64(g) + 0.5) * mult
				bs += float64(h) * (float64(b) + 0.5) * mult
			}
		}
	}
	if tot > 0 {
		v.avg = [3]int{int(rs / float64(tot)), int(gs / float64(tot)), int(bs / float64(tot))}
	} else {
		v.avg = [3]int{mult * (v.r1 + v.r2 + 1) / 2, mult * (v.g1 + v.g2 + 1) / 2, mult * (v.b1 + v.b2 + 1) / 2}
	}
	v.avgSet = true
	return v.avg
}

// vboxQueue pops its largest box by less, ties going to the last pushed, as
// the JS PQueue (a stable ascending sort, popping the end) does.
type vboxQueue struct {
	boxes []*vbox
	key   func(*vbox) int
}

func (q *vboxQueue) push(v *vbox) { q.boxes = append(q.boxes, v) }
func (q *vboxQueue) pop() *vbox {
	sort.SliceStable(q.boxes, func(i, j int) bool { return q.key(q.boxes[i]) < q.key(q.boxes[j]) })
	v := q.boxes[len(q.boxes)-1]
	q.boxes = q.boxes[:len(q.boxes)-1]
	return v
}

func medianCut(v *vbox) (*vbox, *vbox) {
	if v.cnt() == 0 {
		return nil, nil
	}
	if v.cnt() == 1 {
		return v.copy(), nil
	}
	rw, gw, bw := v.r2-v.r1+1, v.g2-v.g1+1, v.b2-v.b1+1
	maxw := max(rw, gw, bw)
	// lo and hi are the bounds of the axis cut along.
	var lo, hi *int
	var sumAt func(i int) int
	switch maxw {
	case rw:
		lo, hi = &v.r1, &v.r2
		sumAt = func(i int) (s int) {
			for g := v.g1; g <= v.g2; g++ {
				for b := v.b1; b <= v.b2; b++ {
					s += v.histo[mmcqIndex(i, g, b)]
				}
			}
			return
		}
	case gw:
		lo, hi = &v.g1, &v.g2
		sumAt = func(i int) (s int) {
			for r := v.r1; r <= v.r2; r++ {
				for b := v.b1; b <= v.b2; b++ {
					s += v.histo[mmcqIndex(r, i, b)]
				}
			}
			return
		}
	default:
		lo, hi = &v.b1, &v.b2
		sumAt = func(i int) (s int) {
			for r := v.r1; r <= v.r2; r++ {
				for g := v.g1; g <= v.g2; g++ {
					s += v.histo[mmcqIndex(r, g, i)]
				}
			}
			return
		}
	}
	// partial and ahead are 0 (falsy in JS) outside the axis range.
	var partial, ahead [1 << mmcqSigBits]int
	total := 0
	for i := *lo; i <= *hi; i++ {
		total += sumAt(i)
		partial[i] = total
	}
	for i := *lo; i <= *hi; i++ {
		ahead[i] = total - partial[i]
	}
	at := func(a *[1 << mmcqSigBits]int, i int) int {
		if i < 0 || i >= len(a) {
			return 0
		}
		return a[i]
	}
	v1, v2 := *lo, *hi
	for i := v1; i <= v2; i++ {
		if float64(partial[i]) <= float64(total)/2 {
			continue
		}
		left, right := i-v1, v2-i
		var d2 int
		if left <= right {
			d2 = min(v2-1, int(float64(i)+float64(right)/2))
		} else {
			d2 = max(v1, int(float64(i)-1-float64(left)/2))
		}
		for at(&partial, d2) == 0 && d2 < len(partial) {
			d2++
		}
		count2 := at(&ahead, d2)
		for count2 == 0 && at(&partial, d2-1) != 0 {
			d2--
			count2 = at(&ahead, d2)
		}
		a, b := v.copy(), v.copy()
		// Set the cut on the same axis of each copy.
		set := func(box *vbox, hiSide bool, val int) {
			switch lo {
			case &v.r1:
				if hiSide {
					box.r2 = val
				} else {
					box.r1 = val
				}
			case &v.g1:
				if hiSide {
					box.g2 = val
				} else {
					box.g1 = val
				}
			default:
				if hiSide {
					box.b2 = val
				} else {
					box.b1 = val
				}
			}
		}
		set(a, true, d2)
		set(b, false, d2+1)
		return a, b
	}
	return nil, nil
}

// quantize returns the palette of pixels in at most maxColors colors, the
// most present first, or nil if there are no pixels.
func quantize(pixels [][3]uint8, maxColors int) [][3]int {
	if len(pixels) == 0 || maxColors < 2 || maxColors > 256 {
		return nil
	}
	histo := make([]int, 1<<(3*mmcqSigBits))
	rmin, gmin, bmin := 1<<30, 1<<30, 1<<30
	rmax, gmax, bmax := 0, 0, 0
	for _, p := range pixels {
		r, g, b := int(p[0])>>mmcqRShift, int(p[1])>>mmcqRShift, int(p[2])>>mmcqRShift
		histo[mmcqIndex(r, g, b)]++
		rmin, rmax = min(rmin, r), max(rmax, r)
		gmin, gmax = min(gmin, g), max(gmax, g)
		bmin, bmax = min(bmin, b), max(bmax, b)
	}
	start := &vbox{r1: rmin, r2: rmax, g1: gmin, g2: gmax, b1: bmin, b2: bmax, histo: histo}
	iter := func(q *vboxQueue, target float64) {
		ncolors, niters := 1, 0
		for niters < mmcqMaxIter {
			v := q.pop()
			if v.cnt() == 0 {
				q.push(v)
				niters++
				continue
			}
			a, b := medianCut(v)
			if a == nil {
				return
			}
			q.push(a)
			if b != nil {
				q.push(b)
				ncolors++
			}
			if float64(ncolors) >= target {
				return
			}
			if niters > mmcqMaxIter {
				return
			}
			niters++
		}
	}
	pq := &vboxQueue{key: func(v *vbox) int { return v.cnt() }}
	pq.push(start)
	iter(pq, mmcqFractPop*float64(maxColors))
	pq2 := &vboxQueue{key: func(v *vbox) int { return v.cnt() * v.volume() }}
	for len(pq.boxes) > 0 {
		pq2.push(pq.pop())
	}
	iter(pq2, float64(maxColors-len(pq2.boxes)))
	var out [][3]int
	for len(pq2.boxes) > 0 {
		out = append(out, pq2.pop().average())
	}
	return out
}

// primaryColor is the first color of img's palette as Discord takes it from
// an avatar, black if no pixel counts.
func primaryColor(img image.Image) color.NRGBA {
	b := img.Bounds()
	w, n := b.Dx(), b.Dx()*b.Dy()
	pixels := make([][3]uint8, 0, n/paletteStep+1)
	for i := 0; i < n; i += paletteStep {
		c := color.NRGBAModel.Convert(img.At(b.Min.X+i%w, b.Min.Y+i/w)).(color.NRGBA)
		if c.A < 125 || (c.R > 250 && c.G > 250 && c.B > 250) {
			continue
		}
		pixels = append(pixels, [3]uint8{c.R, c.G, c.B})
	}
	pal := quantize(pixels, paletteColors)
	if len(pal) == 0 {
		return color.NRGBA{A: 0xff}
	}
	p := pal[0]
	return color.NRGBA{R: uint8(p[0]), G: uint8(p[1]), B: uint8(p[2]), A: 0xff}
}
