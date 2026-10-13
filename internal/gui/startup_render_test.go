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
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// Preview the English startup and sign-in screens without credentials,
// network requests, or a desktop window.
func TestRenderStartup(t *testing.T) {
	out := os.Getenv("LINKCORD_RENDER")
	if out == "" {
		t.Skip("LINKCORD_RENDER is not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	const w, h = 1100, 680
	u := &ui{win: new(app.Window), th: newTheme(), focused: true}
	u.startup.status = "Checking for updates…"
	u.login = &loginView{u: u, tab: tabPassword}
	u.login.email.SingleLine = true
	u.login.password.SingleLine = true
	u.login.password.Mask = '•'
	for _, tc := range []struct {
		name   string
		screen screen
	}{
		{"english-startup", screenStartup},
		{"english-login", screenLogin},
	} {
		u.screen = tc.screen
		win, err := headless.NewWindow(w, h)
		if err != nil {
			t.Fatal(err)
		}
		var router input.Router
		var ops op.Ops
		for range 3 {
			ops.Reset()
			gtx := layout.Context{
				Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Now: time.Now(), Source: router.Source(),
				Constraints: layout.Exact(image.Pt(w, h)),
			}
			u.layout(gtx)
			router.Frame(&ops)
		}
		if err := win.Frame(&ops); err != nil {
			win.Release()
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if err := win.Screenshot(img); err != nil {
			win.Release()
			t.Fatal(err)
		}
		win.Release()
		f, err := os.Create(filepath.Join(out, tc.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, img)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
