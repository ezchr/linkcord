package gui

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// testSound is silence, a loud tone, silence again, then speech-like sound
// that rises and falls, as 48 kHz stereo.
func testSound() []int16 {
	var pcm []int16
	add := func(seconds float64, amp func(t float64) float64) {
		n := int(seconds * 48000)
		for i := range n {
			t := float64(i) / 48000
			v := int16(amp(t) * 32767 * math.Sin(2*math.Pi*220*t))
			pcm = append(pcm, v, v)
		}
	}
	silence := func(float64) float64 { return 0.0005 }
	add(1.5, silence)
	add(1, func(float64) float64 { return 0.8 })
	add(1, silence)
	add(2.5, func(t float64) float64 { return 0.05 + 0.4*math.Abs(math.Sin(t*4)) })
	return pcm
}

func TestAudioTrimLevelsAndKept(t *testing.T) {
	tr := newAudioTrim(testSound())
	if d := tr.duration(); d != 6*time.Second {
		t.Fatalf("duration %v", d)
	}
	bars := tr.levels(60)
	// The first slices are silence, those of the tone loud.
	if bars[2] > 0.2 || bars[17] < 0.8 {
		t.Fatalf("silence %.2f, noise %.2f", bars[2], bars[17])
	}
	tr.start, tr.end = 0.25, 0.5
	kept := tr.kept()
	if len(kept)%2 != 0 || len(kept) != 3*48000 {
		t.Fatalf("kept %d samples, want %d", len(kept), 3*48000)
	}
}

func TestAudioTrimDrag(t *testing.T) {
	tr := newAudioTrim(testSound())
	var router input.Router
	var ops op.Ops
	const w, h = 600, 40
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now(), Source: router.Source(),
			Constraints: layout.Exact(image.Pt(w, 80))}
		tr.layout(gtx, newTheme(), h)
		router.Frame(&ops)
	}
	frame()
	// Pressed near the end, it moves there, then dragged further in.
	at := func(x float32) f32.Point { return f32.Pt(x, h/2) }
	router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(w * 0.8)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(w * 0.6)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at(w * 0.6)},
	)
	frame()
	if tr.start != 0 || math.Abs(float64(tr.end)-0.6) > 0.01 {
		t.Fatalf("after dragging the end: %.2f to %.2f", tr.start, tr.end)
	}
	// The start cannot pass the end.
	router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(w * 0.1)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(w * 0.95)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at(w * 0.95)},
	)
	frame()
	if tr.start >= tr.end {
		t.Fatalf("start %.2f passed the end %.2f", tr.start, tr.end)
	}
}

// TestAudioTrimRender draws the trimmer to LINKCORD_TRIM_RENDER/trim.png.
func TestAudioTrimRender(t *testing.T) {
	out := os.Getenv("LINKCORD_TRIM_RENDER")
	if out == "" {
		t.Skip("set LINKCORD_TRIM_RENDER to a folder to draw the trimmer")
	}
	const w, h = 640, 90
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	tr := newAudioTrim(testSound())
	tr.start, tr.end = 0.2, 0.9
	tr.playing = time.Now().Add(-time.Second)
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now(),
		Constraints: layout.Exact(image.Pt(w, h))}
	paint.FillShape(&ops, colorChat, clip.Rect{Max: image.Pt(w, h)}.Op())
	layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return tr.layout(gtx, newTheme(), 40)
	})
	if err := win.Frame(&ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(out, "trim.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
