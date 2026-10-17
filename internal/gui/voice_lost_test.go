package gui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// TestLostVoiceRender draws the bar of a voice channel lost to
// LINKCORD_TRIM_RENDER/lost-voice.png.
func TestLostVoiceRender(t *testing.T) {
	out := os.Getenv("LINKCORD_TRIM_RENDER")
	if out == "" {
		t.Skip("set LINKCORD_TRIM_RENDER to a folder to draw it")
	}
	const w, h = 1000, 60
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	uiLang = "es"
	defer func() { uiLang = "" }()
	c := &chatView{u: &ui{th: newTheme(), win: new(app.Window)}}
	c.lostVoice.ch = &discord.Channel{Name: "los pollos hermanos"}
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now(), Constraints: layout.Exact(image.Pt(w, h))}
	c.layoutLostVoice(gtx)
	if err := win.Frame(&ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(out, "lost-voice.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}
