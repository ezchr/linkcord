package gui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRenderDemo draws the demo without a window and saves pictures, to check
// the interface where no screen is at hand. It only runs when
// LINKCORD_RENDER is the folder for the pictures. LINKCORD_RENDER_STEPS is
// what to do, separated by ";":
//
//	move:x,y  click:x,y  shiftclick:x,y  key:Name  text:abc  wait:ms  snap:name
//
// Keys are Return, Escape, Up, Down or Gio's names.
func TestRenderDemo(t *testing.T) {
	out := os.Getenv("LINKCORD_RENDER")
	if out == "" {
		t.Skip("LINKCORD_RENDER is not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	const w, h = 1100, 680
	u := &ui{win: new(app.Window), th: newTheme(), focused: true}
	u.images = newImages(u)
	u.startDemo()

	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	var router input.Router
	var ops op.Ops
	frame := func() {
		u.runPending()
		ops.Reset()
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Now:         time.Now(),
			Constraints: layout.Exact(image.Pt(w, h)),
			Source:      router.Source(),
		}
		u.layout(gtx)
		router.Frame(&ops)
	}
	// Several frames, so that posted work and pictures loading settle.
	settle := func() {
		for range 4 {
			frame()
			time.Sleep(30 * time.Millisecond)
		}
	}
	point := func(arg string) f32.Point {
		x, y, _ := strings.Cut(arg, ",")
		fx, _ := strconv.ParseFloat(x, 32)
		fy, _ := strconv.ParseFloat(y, 32)
		return f32.Pt(float32(fx), float32(fy))
	}
	settle()
	for _, step := range strings.Split(os.Getenv("LINKCORD_RENDER_STEPS"), ";") {
		verb, arg, _ := strings.Cut(strings.TrimSpace(step), ":")
		switch verb {
		case "move":
			router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: point(arg)})
		case "click", "shiftclick":
			var mods key.Modifiers
			if verb == "shiftclick" {
				mods = key.ModShift
			}
			p := point(arg)
			router.Queue(
				pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p},
				pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: p, Buttons: pointer.ButtonPrimary, Modifiers: mods},
				pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p, Modifiers: mods},
			)
		case "key":
			name := key.Name(arg)
			if n, ok := keyNames[arg]; ok {
				name = n
			}
			router.Queue(key.Event{Name: name, State: key.Press}, key.Event{Name: name, State: key.Release})
		case "text":
			router.Queue(key.EditEvent{Text: arg})
		case "wait":
			ms, _ := strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "snap":
			settle()
			if err := win.Frame(&ops); err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(image.Rect(0, 0, w, h))
			if err := win.Screenshot(img); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(out, arg+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
			continue
		default:
			continue
		}
		settle()
	}
}

var keyNames = map[string]key.Name{
	"Return": key.NameReturn,
	"Escape": key.NameEscape,
	"Up":     key.NameUpArrow,
	"Down":   key.NameDownArrow,
}
