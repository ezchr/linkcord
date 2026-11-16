package gui

import (
	"image"
	"math"
)

// The colors of a background picture: Linkcord takes the picture's main hue
// for the surfaces and its brightest clear hue for the accent, and builds
// every color of the interface from the two.

// hsl is a color as hue (0 to 360), saturation and lightness (0 to 1).
type hsl struct{ h, s, l float64 }

func toHSL(c colorT) hsl {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l := (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return hsl{0, 0, l}
	}
	s := d / (1 - math.Abs(2*l-1))
	var h float64
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return hsl{h, min(s, 1), l}
}

func (c hsl) rgb() colorT {
	l, s := min(max(c.l, 0), 1), min(max(c.s, 0), 1)
	h := math.Mod(c.h, 360)
	if h < 0 {
		h += 360
	}
	ch := (1 - math.Abs(2*l-1)) * s
	x := ch * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - ch/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = ch, x
	case h < 120:
		r, g = x, ch
	case h < 180:
		g, b = ch, x
	case h < 240:
		g, b = x, ch
	case h < 300:
		r, b = x, ch
	default:
		r, b = ch, x
	}
	to := func(v float64) uint8 { return uint8(math.Round(min(max(v+m, 0), 1) * 255)) }
	return colorT{R: to(r), G: to(g), B: to(b), A: 0xff}
}

// luminance is the relative luminance of c, as WCAG defines it.
func luminance(c colorT) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast is the WCAG contrast ratio of a and b, from 1 to 21.
func contrast(a, b colorT) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// minContrast is the contrast text needs on what it is written on: WCAG's
// AA for text of normal size.
const minContrast = 4.5

// readable raises the lightness of fg until it has minContrast on every
// color of on, or is white.
func readable(fg hsl, on ...colorT) colorT {
	for ; fg.l < 1; fg.l = min(fg.l+0.01, 1) {
		if contrastAll(fg.rgb(), on) {
			return fg.rgb()
		}
	}
	return fg.rgb()
}

// underText lowers the lightness of bg until text has minContrast on it.
func underText(bg hsl, text colorT) colorT {
	for ; bg.l > 0; bg.l = max(bg.l-0.01, 0) {
		if contrast(bg.rgb(), text) >= minContrast {
			return bg.rgb()
		}
	}
	return bg.rgb()
}

func contrastAll(fg colorT, on []colorT) bool {
	for _, b := range on {
		if contrast(fg, b) < minContrast {
			return false
		}
	}
	return true
}

// pictureSamples are the colors of img on a grid of at most n by n cells,
// each the average of a few of its pixels.
func pictureSamples(img image.Image, n int) []colorT {
	b := img.Bounds()
	if b.Empty() || n <= 0 {
		return nil
	}
	cols, rows := min(n, b.Dx()), min(n, b.Dy())
	out := make([]colorT, 0, cols*rows)
	for j := range rows {
		y0, y1 := b.Min.Y+j*b.Dy()/rows, b.Min.Y+(j+1)*b.Dy()/rows
		for i := range cols {
			x0, x1 := b.Min.X+i*b.Dx()/cols, b.Min.X+(i+1)*b.Dx()/cols
			// At most 4 by 4 pixels of each cell, evenly spread.
			var r, g, bl, k uint32
			for sy := range 4 {
				y := y0 + sy*(y1-y0)/4
				for sx := range 4 {
					x := x0 + sx*(x1-x0)/4
					cr, cg, cb, _ := img.At(x, y).RGBA()
					r, g, bl, k = r+cr>>8, g+cg>>8, bl+cb>>8, k+1
				}
			}
			out = append(out, colorT{R: uint8(r / k), G: uint8(g / k), B: uint8(bl / k), A: 0xff})
		}
	}
	return out
}

// pictureColors are the main hue of the samples and their accent: the
// main is the hue most of the picture leans to, weighted by how colorful
// each part is, with its saturation how colorful the picture is on the
// whole; the accent is the strongest hue among clear colors, with no
// grays. ok is false for a picture with no clear color.
func pictureColors(samples []colorT) (main, accent hsl, ok bool) {
	const bins = 36
	var all, clear [bins]float64
	var chromaSum float64
	for _, c := range samples {
		v := toHSL(c)
		chroma := v.s * (1 - math.Abs(2*v.l-1))
		chromaSum += chroma
		bin := int(v.h/10) % bins
		all[bin] += chroma
		// Near grays, and colors almost black or white, have no hue that
		// shows.
		if chroma > 0.15 && v.l > 0.15 && v.l < 0.9 {
			clear[bin] += chroma * chroma
		}
	}
	if len(samples) == 0 {
		return hsl{}, hsl{}, false
	}
	// A peak counts its neighbours too, as a hue may fall on a border.
	peak := func(h *[bins]float64) (int, float64) {
		best, bestW := 0, 0.0
		for i := range bins {
			w := h[(i+bins-1)%bins] + 2*h[i] + h[(i+1)%bins]
			if w > bestW {
				best, bestW = i, w
			}
		}
		return best, bestW
	}
	// meanHue is the average hue of the samples around bin, and their
	// saturation.
	meanHue := func(bin int, grays bool) (float64, float64) {
		var x, y, s, n float64
		for _, c := range samples {
			v := toHSL(c)
			chroma := v.s * (1 - math.Abs(2*v.l-1))
			if !grays && (chroma <= 0.15 || v.l <= 0.15 || v.l >= 0.9) {
				continue
			}
			d := (int(v.h/10)%bins - bin + bins) % bins
			if d > 1 && d < bins-1 {
				continue
			}
			x += chroma * math.Cos(v.h*math.Pi/180)
			y += chroma * math.Sin(v.h*math.Pi/180)
			s += chroma * v.s
			n += chroma
		}
		if n == 0 {
			return float64(bin*10 + 5), 0
		}
		h := math.Atan2(y, x) * 180 / math.Pi
		if h < 0 {
			h += 360
		}
		return h, s / n
	}
	mb, _ := peak(&all)
	mh, _ := meanHue(mb, true)
	main = hsl{mh, min(chromaSum/float64(len(samples))*1.5, 1), 0.5}
	ab, aw := peak(&clear)
	if aw == 0 {
		return main, hsl{}, false
	}
	ah, as := meanHue(ab, false)
	return main, hsl{ah, as, 0.5}, true
}

// defaultAccent is the accent of a picture with no clear color: Discord's
// blurple.
var defaultAccent = toHSL(rgb(0x5865f2))

// derivePalette builds every color the user can change from a picture's
// samples: dark surfaces tinted with its main hue, a step lighter for what
// is hovered and chosen, text that reads on all of them, and its accent for
// buttons, links and mentions. Colors of status, such as red and green,
// stay as they are. The result is the same for the same samples.
func derivePalette(samples []colorT) map[string]colorT {
	main, accent, ok := pictureColors(samples)
	if !ok {
		accent = defaultAccent
	}
	// The surfaces are only tinted: a strong color behind text tires.
	tint := min(main.s*0.45, 0.28)
	if tint < 0.03 {
		tint = 0
	}
	surface := func(l float64) colorT { return hsl{main.h, tint, l}.rgb() }
	p := map[string]colorT{
		"servers":  surface(0.055),
		"popup":    surface(0.05),
		"sidebar":  surface(0.08),
		"panel":    surface(0.068),
		"menu":     surface(0.07),
		"code":     surface(0.07),
		"chat":     surface(0.105),
		"input":    surface(0.16),
		"hover":    surface(0.145),
		"selected": surface(0.195),
	}
	// Text is checked on every surface it is written on.
	on := []colorT{p["servers"], p["popup"], p["sidebar"], p["panel"], p["menu"], p["code"], p["chat"], p["input"], p["hover"], p["selected"]}
	p["header"] = readable(hsl{main.h, tint * 0.5, 0.95}, on...)
	p["text"] = readable(hsl{main.h, tint * 0.4, 0.86}, on...)
	p["muted"] = readable(hsl{main.h, tint * 0.35, 0.64}, on...)
	p["channel"] = readable(hsl{main.h, tint * 0.3, 0.6}, on...)

	// Buttons carry the brightest text on the accent.
	as := min(max(accent.s, 0.45), 0.85)
	p["accent"] = underText(hsl{accent.h, as, 0.55}, p["header"])
	p["secondaryButton"] = underText(hsl{main.h, tint, 0.34}, p["header"])
	p["link"] = readable(hsl{accent.h, max(as, 0.6), 0.68}, p["chat"], p["hover"], p["selected"])
	p["mentionBg"] = hsl{accent.h, min(as, 0.5), 0.24}.rgb()
	p["mention"] = readable(hsl{accent.h, as, 0.84}, p["mentionBg"])
	return p
}
