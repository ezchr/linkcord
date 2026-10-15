package gui

import (
	"fmt"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
	woff2 "github.com/tdewolff/font"
)

// Nitro lets a user choose the font of their name. The fonts are Discord's
// own, downloaded from Discord the first time someone shown has chosen one
// and kept, then added to the shaper as the fonts of other scripts are.

// nameFonts are Discord's fonts for names, by font_id: the file Discord's
// app loads.
var nameFonts = map[int]string{
	3:  "33d4f12a85e1f736", // Cherry Bomb, "Sakura"
	4:  "e6f5f44abb520735", // Chicle, "Jellybean"
	6:  "c560709c3470bb66", // MuseoModerno, "Modern"
	7:  "52b541f86401a5b6", // Neo Castel, "Medieval"
	8:  "69c735ca5c604de7", // Pixelify, "8Bit"
	10: "8f20cb550d739cea", // Sinistre, "Vampyre"
	12: "27b34a77ba5d693e", // Zilla Slab, "Tempo"
	13: "45ef50dce38a931a", // Playpen Sans, "Monkey Bars"
	14: "4841dace333e9054", // Orbitron, "Mainframe"
	15: "c00df4f25667809b", // New Rocker, "Headbang"
	16: "aa59ae54ce7e540a", // Kalam, "Journal"
}

// needName is the bit of fontNeeds of the name font id, past those of
// scripts.
func needName(id int) uint32 { return 1 << (15 + id) }

// nameTypeface is what the font of id is called in the shaper, apart from
// any other font so that no other text is drawn in it.
func nameTypeface(id int) font.Typeface { return font.Typeface(fmt.Sprintf("Linkcord Name %d", id)) }

func nameFontPath(id int) string {
	return filepath.Join(filepath.Dir(logPath()), "name-fonts", fmt.Sprintf("%d.ttf", id))
}

// nameFontFetching are the fonts being downloaded.
var nameFontFetching sync.Map

// wantNameFont asks for the font id to be added to the shaper, downloading
// it first if it is not kept yet.
func wantNameFont(id int) {
	hash, ok := nameFonts[id]
	if !ok || fontNeeds.Load()&needName(id) != 0 {
		return
	}
	add := func() {
		if old := fontNeeds.Or(needName(id)); old&needName(id) == 0 && onFontNeeds != nil {
			onFontNeeds()
		}
	}
	if _, err := os.Stat(nameFontPath(id)); err == nil {
		add()
		return
	}
	if _, busy := nameFontFetching.LoadOrStore(id, true); busy {
		return
	}
	go func() {
		if err := fetchNameFont(id, hash); err != nil {
			slog.Warn("could not get the font of a name", "font", id, "err", err)
			// Tried again a while later.
			time.AfterFunc(10*time.Minute, func() { nameFontFetching.Delete(id) })
			return
		}
		nameFontFetching.Delete(id)
		add()
	}()
}

// fetchNameFont downloads Discord's WOFF2 font and keeps it as a TrueType
// font, which the shaper reads.
func fetchNameFont(id int, hash string) error {
	resp, err := http.Get("https://discord.com/assets/" + hash + ".woff2")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	sfnt, err := woff2.ParseWOFF2(data)
	if err != nil {
		return err
	}
	p := nameFontPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, sfnt, 0o644)
}

// nameFontLoaded reports whether the shaper has the font id.
func nameFontLoaded(id int) bool { return fontsLoaded.Load()&needName(id) != 0 }

// styledName draws l, a name, as user chose with Nitro: in their font, once
// it is loaded, and in their colors, a gradient between the first two for
// the gradient effect. Names without a style are drawn as they are.
func styledName(gtx layout.Context, l material.LabelStyle, user discord.User) layout.Dimensions {
	st := user.DisplayNameStyles
	if st == nil {
		return l.Layout(gtx)
	}
	if _, ok := nameFonts[st.FontID]; ok {
		wantNameFont(st.FontID)
		if nameFontLoaded(st.FontID) {
			l.Font = font.Font{Typeface: nameTypeface(st.FontID)}
		}
	}
	if len(st.Colors) == 0 {
		return l.Layout(gtx)
	}
	first := nameRGB(st.Colors[0], l.Color.A)
	if st.EffectID != discord.DisplayNameEffectGradient || len(st.Colors) < 2 {
		l.Color = first
		return l.Layout(gtx)
	}
	// The width the gradient spans is the name's, measured first.
	m := op.Record(gtx.Ops)
	dims := l.Layout(gtx)
	m.Stop()
	mat := op.Record(gtx.Ops)
	paint.LinearGradientOp{
		Stop1: f32.Pt(0, 0), Color1: first,
		Stop2: f32.Pt(float32(max(dims.Size.X, 1)), 0), Color2: nameRGB(st.Colors[1], l.Color.A),
	}.Add(gtx.Ops)
	return widget.Label{Alignment: l.Alignment, MaxLines: l.MaxLines, Truncator: l.Truncator}.
		Layout(gtx, l.Shaper, l.Font, l.TextSize, l.Text, mat.Stop())
}

func nameRGB(c uint32, a uint8) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: max(a, 1)}
}

