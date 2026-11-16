package gui

import (
	"image"
	"image/color"
	"maps"
	"testing"
)

// picture is a w by h picture whose pixel at x, y is f(x, y).
func picture(w, h int, f func(x, y int) colorT) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, f(x, y))
		}
	}
	return img
}

// checkReadable checks that text has minContrast on every surface it is
// written on, and on buttons and mentions.
func checkReadable(t *testing.T, p map[string]colorT) {
	t.Helper()
	surfaces := []string{"servers", "popup", "sidebar", "panel", "menu", "code", "chat", "input", "hover", "selected"}
	for _, fg := range []string{"header", "text", "muted", "channel"} {
		for _, bg := range surfaces {
			if r := contrast(p[fg], p[bg]); r < minContrast {
				t.Errorf("%s on %s: contrast %.2f, want %.1f", fg, bg, r, minContrast)
			}
		}
	}
	pairs := [][2]string{{"header", "accent"}, {"header", "secondaryButton"}, {"link", "chat"}, {"link", "hover"}, {"mention", "mentionBg"}}
	for _, pr := range pairs {
		if r := contrast(p[pr[0]], p[pr[1]]); r < minContrast {
			t.Errorf("%s on %s: contrast %.2f, want %.1f", pr[0], pr[1], r, minContrast)
		}
	}
	// Every color a preset can set is set.
	for _, g := range themeGroups {
		for _, c := range g.colors {
			if _, ok := p[c.key]; !ok && !keepsDefault(c.key) {
				t.Errorf("no color for %s", c.key)
			}
		}
	}
}

// keepsDefault reports whether the colors of a picture leave key as it is:
// the colors of status, which mean the same on any theme.
func keepsDefault(key string) bool {
	switch key {
	case "green", "greenButton", "red", "yellow", "offline", "boost", "boostChip", "mentioned":
		return true
	}
	return false
}

func TestPaletteContrast(t *testing.T) {
	pictures := map[string]image.Image{
		// A sunset: orange above, purple below, a blue spot.
		"sunset": picture(120, 80, func(x, y int) colorT {
			if x > 90 && y < 20 {
				return rgb(0x1e90ff)
			}
			if y < 40 {
				return rgb(0xf08030)
			}
			return rgb(0x502060)
		}),
		"grey":   picture(64, 64, func(x, y int) colorT { return colorT{R: 128, G: 128, B: 128, A: 255} }),
		"white":  picture(64, 64, func(x, y int) colorT { return colorT{R: 250, G: 250, B: 248, A: 255} }),
		"pastel": picture(64, 64, func(x, y int) colorT { return rgb(0xffe4f0) }),
		"black":  picture(64, 64, func(x, y int) colorT { return colorT{A: 255} }),
		"green":  picture(64, 64, func(x, y int) colorT { return rgb(0x10c020) }),
		"yellow": picture(64, 64, func(x, y int) colorT { return rgb(0xffee00) }),
		"noise": picture(97, 61, func(x, y int) colorT {
			v := uint32(x*7919+y*104729) * 2654435761
			return rgb(v >> 8)
		}),
	}
	for name, img := range pictures {
		t.Run(name, func(t *testing.T) {
			checkReadable(t, derivePalette(pictureSamples(img, 48)))
		})
	}
}

func TestPaletteDeterministic(t *testing.T) {
	img := picture(200, 150, func(x, y int) colorT {
		return colorT{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255}
	})
	a := derivePalette(pictureSamples(img, 48))
	b := derivePalette(pictureSamples(img, 48))
	if !maps.Equal(a, b) {
		t.Fatal("the same picture gave other colors")
	}
}

// TestPaletteGrey checks that a picture with no color gives gray surfaces
// and the default accent.
func TestPaletteGrey(t *testing.T) {
	img := picture(64, 64, func(x, y int) colorT { v := uint8(60 + x); return colorT{R: v, G: v, B: v, A: 255} })
	p := derivePalette(pictureSamples(img, 48))
	for _, k := range []string{"servers", "sidebar", "chat", "input"} {
		if c := p[k]; c.R != c.G || c.G != c.B {
			t.Errorf("%s is %v, want a gray", k, c)
		}
	}
	if h := toHSL(p["accent"]).h; h < defaultAccent.h-5 || h > defaultAccent.h+5 {
		t.Errorf("accent hue %.0f, want about %.0f", h, defaultAccent.h)
	}
}

// TestPaletteLight checks that a light picture still gives a dark theme.
func TestPaletteLight(t *testing.T) {
	img := picture(64, 64, func(x, y int) colorT { return rgb(0xf4f0e8) })
	p := derivePalette(pictureSamples(img, 48))
	for _, k := range []string{"servers", "sidebar", "chat", "popup", "menu"} {
		if l := luminance(p[k]); l > 0.05 {
			t.Errorf("%s has luminance %.3f; want a dark surface", k, l)
		}
	}
	if luminance(p["text"]) < 0.5 {
		t.Error("text is not light")
	}
}

// TestPaletteAccentHue checks that the accent is the picture's clear color,
// and the surfaces lean to its main hue.
func TestPaletteAccentHue(t *testing.T) {
	// Mostly dark blue, with a red spot.
	img := picture(100, 100, func(x, y int) colorT {
		if x < 15 && y < 15 {
			return rgb(0xe02020)
		}
		return rgb(0x203070)
	})
	main, accent, ok := pictureColors(pictureSamples(img, 48))
	if !ok {
		t.Fatal("no accent found")
	}
	if main.h < 210 || main.h > 240 {
		t.Errorf("main hue %.0f, want blue", main.h)
	}
	if accent.h > 15 && accent.h < 345 && (accent.h < 210 || accent.h > 240) {
		t.Errorf("accent hue %.0f, want red or blue", accent.h)
	}
}

func TestHSLRoundTrip(t *testing.T) {
	for _, c := range []uint32{0x000000, 0xffffff, 0x5865f2, 0x23a55a, 0xda373c, 0x808080, 0x123456} {
		got := toHSL(rgb(c)).rgb()
		if want := rgb(c); got != want {
			t.Errorf("%06x came back as %v", c, got)
		}
	}
}

func TestMinPictureDim(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	text := []colorT{rgb(0xdbdee1), rgb(0x949ba4)}
	d := minPictureDim(text, rgb(0x202024), paneAlphaChat, white)
	if d <= 0 || d >= 1 {
		t.Fatalf("a white picture needs some dimming, got %.2f", d)
	}
	// Dimmed that much, the text reads; a little less, it does not.
	mix := func(dim float64) float64 {
		return paneAlphaChat*luminance(rgb(0x202024)) + (1-paneAlphaChat)*(1-dim)*luminance(white)
	}
	ratio := func(dim float64) float64 { return (luminance(rgb(0x949ba4)) + 0.05) / (mix(dim) + 0.05) }
	if r := ratio(d); r < minContrast-1e-6 {
		t.Errorf("at %.2f the contrast is %.2f", d, r)
	}
	if r := ratio(d - 0.05); r >= minContrast {
		t.Errorf("at %.2f the contrast is already %.2f", d-0.05, r)
	}
	if d := minPictureDim(text, rgb(0x202024), paneAlphaChat, colorT{A: 255}); d != 0 {
		t.Errorf("a black picture needs no dimming, got %.2f", d)
	}
}

func TestCoverImage(t *testing.T) {
	src := picture(400, 100, func(x, y int) colorT {
		if x < 150 || x >= 250 {
			return rgb(0xff0000)
		}
		return rgb(0x00ff00)
	})
	// A square window takes the middle of the wide picture.
	img := coverImage(src, image.Pt(50, 50), 1)
	if img.Bounds().Size() != image.Pt(50, 50) {
		t.Fatalf("size %v", img.Bounds().Size())
	}
	if c := img.RGBAAt(25, 25); c.G < 200 || c.R > 50 {
		t.Errorf("middle is %v, want green", c)
	}
	// Never larger than the part cut.
	if img := coverImage(src, image.Pt(1000, 1000), 1); img.Bounds().Dx() > 100 {
		t.Errorf("small picture grown to %v", img.Bounds())
	}
	if img := coverImage(src, image.Pt(80, 80), 4); img.Bounds().Size() != image.Pt(20, 20) {
		t.Errorf("shrunk to %v, want 20x20", img.Bounds().Size())
	}
}

func TestBoxBlur(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	// A white square of 4 by 4 in the middle.
	for y := 8; y < 12; y++ {
		for x := 8; x < 12; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	boxBlur(img, 2)
	if c := img.RGBAAt(10, 10); c.R == 255 || c.R == 0 {
		t.Errorf("center %v not spread", c)
	}
	if c := img.RGBAAt(14, 10); c.R == 0 {
		t.Error("neighbour got nothing")
	}
	if c := img.RGBAAt(0, 0); c.R != 0 {
		t.Errorf("corner %v got light from afar", c)
	}
}
