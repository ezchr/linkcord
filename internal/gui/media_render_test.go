package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

type mediaFixtureTransport struct {
	png, apng, gif []byte
}

func (f mediaFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	data := f.png
	switch {
	case strings.Contains(r.URL.Path, "/stickers/2.png"):
		data = f.apng
	case strings.HasSuffix(r.URL.Path, ".gif"):
		data = f.gif
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(data)), Request: r,
	}, nil
}

// Exercise the actual message body, including sticker-only messages and
// thumbnail-less videos, without Discord, a login, or a desktop window.
func TestRenderMedia(t *testing.T) {
	out := os.Getenv("LINKCORD_RENDER")
	if out == "" {
		t.Skip("LINKCORD_RENDER is not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	poster := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := range 180 {
		for x := range 320 {
			poster.SetRGBA(x, y, color.RGBA{R: uint8(x / 2), G: uint8(y), B: 130, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, poster); err != nil {
		t.Fatal(err)
	}
	u := &ui{win: new(app.Window), th: newTheme(), focused: true}
	u.images = &images{
		entries: make(map[string]*imageEntry),
		sem:     make(chan struct{}, 6), decode: make(chan struct{}, 2),
		client: &http.Client{Transport: mediaFixtureTransport{
			png: encoded.Bytes(), apng: compositionAPNG(t, true),
			gif: testGIF(t, 4, 8, 8, 5),
		}},
	}
	u.startDemo()
	c := u.chat
	if c.selChannel == nil {
		t.Fatal("demo did not select a channel")
	}
	author := discord.User{ID: 1, Username: "Media test", Discriminator: "0"}
	message := func(id discord.MessageID, content string) discord.Message {
		return discord.Message{ID: id, Author: author, Content: content, Timestamp: discord.NewTimestamp(time.Now())}
	}
	pngSticker := message(1, "PNG sticker + Lottie fallback")
	pngSticker.Stickers = []discord.StickerItem{
		{ID: 1, Name: "PNG", FormatType: discord.StickerFormatPNG},
		{ID: 4, Name: "Wave", FormatType: discord.StickerFormatLottie},
	}
	animatedSticker := message(2, "Animated APNG sticker")
	animatedSticker.Stickers = []discord.StickerItem{{ID: 2, Name: "APNG", FormatType: discord.StickerFormatAPNG}}
	tenor := message(3, "Tenor GIF")
	tenor.Embeds = []discord.Embed{{
		Type: discord.GIFVEmbed, URL: "https://tenor.com/view/fixture",
		Video:     &discord.EmbedVideo{URL: "https://media.tenor.com/fixture/AAAAe/animation.mp4", Width: 320, Height: 180},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.test/still.png", Width: 320, Height: 180},
	}}
	video := message(4, "Video embed with thumbnail")
	video.Embeds = []discord.Embed{{
		Type: discord.VideoEmbed, Title: "Video preview", URL: "https://example.test/watch",
		Video:     &discord.EmbedVideo{URL: "https://example.test/clip.mp4", Width: 320, Height: 180},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.test/poster.png", Width: 320, Height: 180},
	}}
	noThumb := message(5, "Video without thumbnail")
	noThumb.Attachments = []discord.Attachment{{Filename: "clip.mp4", ContentType: "video/mp4", URL: "https://example.test/clip.mp4", Width: 320, Height: 180}}
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"media-normal", 1000, 830},
		{"media-narrow", 580, 900},
	} {
		win, err := headless.NewWindow(tc.w, tc.h)
		if err != nil {
			t.Fatal(err)
		}
		var router input.Router
		var ops op.Ops
		for range 12 {
			ops.Reset()
			gtx := layout.Context{
				Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Now: time.Now(), Source: router.Source(),
				Constraints: layout.Exact(image.Pt(tc.w, tc.h)),
			}
			layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutMessageBody(gtx, pngSticker, false, "Media test", colorHeader, roleLook{})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutMessageBody(gtx, animatedSticker, false, "Media test", colorHeader, roleLook{})
						}),
					)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutMessageBody(gtx, tenor, false, "Media test", colorHeader, roleLook{})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutMessageBody(gtx, video, false, "Media test", colorHeader, roleLook{})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutMessageBody(gtx, noThumb, false, "Media test", colorHeader, roleLook{})
						}),
					)
				}),
			)
			router.Frame(&ops)
			time.Sleep(20 * time.Millisecond)
		}
		if err := win.Frame(&ops); err != nil {
			win.Release()
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
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
