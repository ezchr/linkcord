package gui

import (
	"image"
	"image/color"
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
	"gioui.org/op/paint"
	"gioui.org/unit"
)

func TestRenderVideoControls(t *testing.T) {
	out := os.Getenv("LINKCORD_RENDER")
	if out == "" {
		t.Skip("LINKCORD_RENDER is not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	u := &ui{win: new(app.Window), th: newTheme()}
	u.images = &images{entries: make(map[string]*imageEntry)}
	u.startDemo()
	picture := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := range 360 {
		for x := range 640 {
			picture.SetRGBA(x, y, color.RGBA{R: uint8(x / 3), G: uint8(y / 2), B: 100, A: 255})
		}
	}
	frame := paint.NewImageOp(picture)
	u.chat.video = &videoViewer{title: "Video playback", frame: &frame, status: "2.5 s", cancel: func() {}}
	for _, tc := range []struct {
		name    string
		size    image.Point
		profile bool
	}{
		{"video-wide", image.Pt(1000, 700), false},
		{"video-narrow", image.Pt(580, 700), false},
		{"image-viewer", image.Pt(1000, 700), false},
		{"gif-viewer", image.Pt(1000, 700), false},
		{"performance-menu", image.Pt(420, 260), true},
	} {
		win, err := headless.NewWindow(tc.size.X, tc.size.Y)
		if err != nil {
			t.Fatal(err)
		}
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{Ops: &ops, Source: router.Source(), Now: time.Now(),
			Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(tc.size)}
		if tc.profile {
			u.chat.layoutSelfMenu(gtx, &profileView{self: true})
		} else if tc.name == "image-viewer" || tc.name == "gif-viewer" {
			raw := "https://example.test/fixture.gif"
			entry := &imageEntry{state: imageReady, op: frame}
			if tc.name == "gif-viewer" {
				entry.frames = []paint.ImageOp{frame, frame}
				entry.delays = []time.Duration{100 * time.Millisecond, 100 * time.Millisecond}
				entry.cycle = 200 * time.Millisecond
			}
			u.images.entries["640:"+raw] = entry
			u.chat.viewer = &imageViewer{original: raw, openURL: raw, w: 640, h: 360, zoom: 1}
			u.chat.layoutViewer(gtx)
		} else {
			u.chat.layoutVideoViewer(gtx)
		}
		router.Frame(&ops)
		if err := win.Frame(&ops); err != nil {
			win.Release()
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: tc.size})
		err = win.Screenshot(img)
		win.Release()
		if err != nil {
			t.Fatal(err)
		}
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
