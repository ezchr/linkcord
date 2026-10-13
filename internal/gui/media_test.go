package gui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestTenorGIFURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://media.tenor.com/abc/AAAAe/hello.mp4", "https://media.tenor.com/abc/AAAAC/hello.gif"},
		{"https://media.tenor.com/abcAAAAe/hello.mp4", "https://media.tenor.com/abcAAAAC/hello.gif"},
		{"https://media.tenor.com/abcAAAAC/hello.gif", "https://media.tenor.com/abcAAAAC/hello.gif"},
		{"https://media.tenor.com/l9jRDAA9Di8AAAPo/salbaitery.mp4", "https://media.tenor.com/l9jRDAA9Di8AAAAC/salbaitery.gif"},
		{"https://media.tenor.com/DzsWJ3ohKfEAAAAM/salbaitery.gif", "https://media.tenor.com/DzsWJ3ohKfEAAAAC/salbaitery.gif"},
		{"https://media.tenor.com/abc/AAAAC/hello.gif", "https://media.tenor.com/abc/AAAAC/hello.gif"},
		{"https://media.tenor.com/abc/AAAAe/hello.png?foo=bar", "https://media.tenor.com/abc/AAAAC/hello.gif?foo=bar"},
		{"https://tenor.com/view/hello-123", ""},
		{"https://media.tenor.com.evil.test/abc/AAAAe/hello.mp4", ""},
		{"https://images-ext-1.discordapp.net/external/x/https/media.tenor.com/abc/AAAAe/hello.png", ""},
		{"https://media.tenor.com/abc/other/hello.mp4", ""},
	} {
		if got := tenorGIFURL(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMediaURL(t *testing.T) {
	raw := "https://media.discordapp.net/attachments/1/2/test.png?ex=abc&hm=def&width=999"
	got, err := url.Parse(mediaURL(raw, "", 320, 200))
	if err != nil {
		t.Fatal(err)
	}
	q := got.Query()
	if q.Get("ex") != "abc" || q.Get("hm") != "def" || q.Get("width") != "320" || q.Get("height") != "200" || len(q["width"]) != 1 {
		t.Fatalf("signed query or resize damaged: %s", got)
	}
	for _, raw := range []string{
		"https://media.tenor.com/abc/AAAAC/hello.gif",
		"https://example.org/picture.png?width=10",
		"https://cdn.discordapp.com.evil.test/image.png",
	} {
		if got := mediaURL("", raw, 320, 200); got != raw {
			t.Errorf("external URL changed: %s", got)
		}
	}
	if got := mediaURL("", "", 320, 200); got != "" {
		t.Fatal(got)
	}
}

func TestEmbedPreview(t *testing.T) {
	tenor := discord.Embed{
		Type: discord.GIFVEmbed, URL: "https://tenor.com/view/hello-123",
		Video:     &discord.EmbedVideo{URL: "https://media.tenor.com/abc/AAAAe/hello.mp4", Width: 640, Height: 360},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.org/still.png"},
	}
	p, ok := embedPreview(tenor)
	if !ok || p.video || p.proxy != "" || p.original != "https://media.tenor.com/abc/AAAAC/hello.gif" || p.openURL != string(tenor.URL) || p.width != 640 {
		t.Fatalf("Tenor was not routed to animation: %+v, %v", p, ok)
	}
	e := discord.Embed{
		Type:      discord.ImageEmbed,
		Image:     &discord.EmbedImage{URL: "https://example.org/full.png", Width: 500, Height: 300},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.org/thumb.png"},
	}
	p, ok = embedPreview(e)
	if !ok || p.original != string(e.Image.URL) || p.openURL != string(e.Image.URL) {
		t.Fatalf("full image not selected: %+v", p)
	}
	e = discord.Embed{
		Type: discord.VideoEmbed, URL: "https://example.org/watch/123",
		Video:     &discord.EmbedVideo{URL: "https://example.org/video.mp4", Width: 640, Height: 360},
		Thumbnail: &discord.EmbedThumbnail{URL: "https://example.org/poster.jpg"},
	}
	p, ok = embedPreview(e)
	if !ok || !p.video || p.original != string(e.Thumbnail.URL) || p.openURL != string(e.URL) || p.playURL != string(e.Video.URL) {
		t.Fatalf("video thumbnail or watch link lost: %+v", p)
	}
	e.Thumbnail, e.URL = nil, ""
	p, ok = embedPreview(e)
	if !ok || !p.video || p.original != "" || p.proxy != "" || p.openURL != string(e.Video.URL) {
		t.Fatalf("thumbnail-less video should remain clickable: %+v", p)
	}
	if _, ok := embedPreview(discord.Embed{Title: "Article", URL: "https://example.org"}); ok {
		t.Fatal("article misclassified as media")
	}
}

func TestStickerMediaURL(t *testing.T) {
	for _, tc := range []struct {
		format discord.StickerFormatType
		suffix string
	}{
		{discord.StickerFormatPNG, ".png"},
		{discord.StickerFormatAPNG, ".png"},
		{stickerFormatGIF, ".gif"},
		{discord.StickerFormatLottie, ""},
		{99, ""},
	} {
		got := stickerMediaURL(discord.StickerItem{ID: 123, FormatType: tc.format})
		if tc.suffix == "" {
			if got != "" {
				t.Errorf("unsupported format %d should use fallback", tc.format)
			}
		} else if !strings.HasSuffix(got, "/123"+tc.suffix) {
			t.Errorf("format %d: %s", tc.format, got)
		}
	}
	if got := stickerMediaURL(discord.StickerItem{FormatType: discord.StickerFormatPNG}); got != "" {
		t.Fatal("invalid sticker ID accepted")
	}
}

func TestVideoAttachments(t *testing.T) {
	for _, a := range []discord.Attachment{
		{Filename: "CLIP.MP4"}, {Filename: "clip.webm"}, {Filename: "file", ContentType: "video/mp4"},
	} {
		if !isVideo(a) {
			t.Errorf("video not recognized: %+v", a)
		}
	}
	if isVideo(discord.Attachment{Filename: "document.pdf"}) {
		t.Fatal("non-video recognized as video")
	}
	raw := "https://cdn.discordapp.com/attachments/1/2/clip.mp4?ex=abc&hm=def"
	p, err := url.Parse(videoPosterURL(raw))
	if err != nil || p.Hostname() != "media.discordapp.net" || p.Query().Get("format") != "webp" || p.Query().Get("hm") != "def" {
		t.Fatalf("invalid poster URL: %v, %v", p, err)
	}
	if videoPosterURL("https://example.org/large.mp4") != "" {
		t.Fatal("external video would be downloaded as an image")
	}
}
