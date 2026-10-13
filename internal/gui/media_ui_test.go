package gui

import (
	"image"
	"net/url"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

func TestGIFPreviewKeepsAnimation(t *testing.T) {
	original := "https://cdn.discordapp.com/attachments/1/2/animated.gif?ex=abc&hm=def&format=webp&animated=false"
	raw := mediaURL("https://media.discordapp.net/attachments/1/2/poster.webp", original, 320, 180)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/attachments/1/2/animated.gif" || u.Query().Get("format") != "" ||
		u.Query().Get("animated") != "true" || u.Query().Get("hm") != "def" {
		t.Fatalf("animation replaced by static rendition: %s", raw)
	}
	e := discord.Embed{
		Type: discord.GIFVEmbed, URL: "https://example.org/gif/123",
		Video:     &discord.EmbedVideo{URL: "https://example.org/animation.mp4", Width: 320, Height: 180},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.org/poster.jpg"},
	}
	p, ok := embedPreview(e)
	if !ok || p.video || !isGIFVideoURL(p.original) || p.width != 320 || p.openURL != string(e.URL) {
		t.Fatalf("GIFV still needs a play click: %+v", p)
	}
	e.Image = &discord.EmbedImage{URL: "https://example.org/animation.gif", Width: 320, Height: 180}
	p, ok = embedPreview(e)
	if !ok || p.video || p.original != string(e.Image.URL) {
		t.Fatalf("real GIF was not preferred: %+v", p)
	}
}

func TestPaneDrag(t *testing.T) {
	for _, direction := range []float32{1, -1} {
		// The edge starts at x 312 with 240 dp: the pane's fixed side is 240
		// to the left of it, or to the right for the members.
		r := paneResize{origin: 312 - direction*240}
		width := unit.Dp(240)
		var router input.Router
		var ops op.Ops
		frame := func() bool {
			ops.Reset()
			gtx := layout.Context{
				Ops: &ops, Source: router.Source(), Now: time.Now(),
				Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Exact(image.Pt(1000, 700)),
			}
			saved := r.update(gtx, &width, direction)
			// Move the hit region with the pane, as the real layout does.
			r.layout(gtx, 312+int(direction*(float32(width)-240)))
			router.Frame(&ops)
			return saved
		}
		frame()
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, PointerID: 1,
			Position: f32.Pt(312, 100), Buttons: pointer.ButtonPrimary})
		frame()
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, PointerID: 1,
			Position: f32.Pt(372, 100), Buttons: pointer.ButtonPrimary})
		frame()
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, PointerID: 1,
			Position: f32.Pt(392, 100), Buttons: pointer.ButtonPrimary})
		frame()
		want := unit.Dp(240 + direction*80)
		if width != want {
			t.Fatalf("direction %v: width %v, want %v", direction, width, want)
		}
		router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, PointerID: 1,
			Position: f32.Pt(392, 100)})
		if !frame() || r.dragging {
			t.Fatal("drag release did not finish and request persistence")
		}
	}
}
