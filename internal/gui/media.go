package gui

import (
	"image"
	"net/url"
	"path"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconPlay = mustIcon(icons.AVPlayArrow)

// Arikawa predates GIF stickers, which Discord assigns format 4.
const stickerFormatGIF discord.StickerFormatType = 4

type mediaPreview struct {
	proxy, original, openURL string
	width, height            int
	video                    bool
	title                    string
}

func discordMediaHost(host string) bool {
	switch strings.ToLower(host) {
	case "media.discordapp.net", "cdn.discordapp.com", "cdn.discordapp.net":
		return true
	}
	return false
}

// tenorGIFURL converts only known Tenor media variants, never a share page
// or a Discord proxy whose path contains someone else's URL.
func tenorGIFURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "media.tenor.com") || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	switch {
	case len(parts) == 4 && parts[1] != "" && parts[3] != "":
		switch parts[2] {
		case "AAAAe", "AAAPo", "AAAPs", "AAAP1", "AAAA1", "AAAAM":
			parts[2] = "AAAAC"
		case "AAAAC":
		default:
			return ""
		}
	case len(parts) == 3 && parts[2] != "" && len(parts[1]) > 5:
		suffix := parts[1][len(parts[1])-5:]
		switch suffix {
		case "AAAAe", "AAAPo", "AAAPs", "AAAP1", "AAAA1", "AAAAM", "AAAAC":
			parts[1] = parts[1][:len(parts[1])-5] + "AAAAC"
		default:
			return ""
		}
	default:
		return ""
	}
	last := len(parts) - 1
	parts[last] = strings.TrimSuffix(parts[last], path.Ext(parts[last])) + ".gif"
	u.Path = strings.Join(parts, "/")
	u.RawPath = ""
	u.Scheme = "https"
	return u.String()
}

func embedPreview(e discord.Embed) (mediaPreview, bool) {
	p := mediaPreview{openURL: string(e.URL), title: e.Title}
	if e.Type == discord.GIFVEmbed || e.Type == discord.ImageEmbed {
		var candidates []string
		if e.Video != nil {
			candidates = append(candidates, string(e.Video.URL))
		}
		if e.Image != nil {
			candidates = append(candidates, string(e.Image.URL))
		}
		if e.Thumbnail != nil {
			candidates = append(candidates, string(e.Thumbnail.URL))
		}
		candidates = append(candidates, string(e.URL))
		for _, candidate := range candidates {
			if gif := tenorGIFURL(candidate); gif != "" {
				p.original = gif
				switch {
				case e.Video != nil:
					p.width, p.height = int(e.Video.Width), int(e.Video.Height)
				case e.Image != nil:
					p.width, p.height = int(e.Image.Width), int(e.Image.Height)
				case e.Thumbnail != nil:
					p.width, p.height = int(e.Thumbnail.Width), int(e.Thumbnail.Height)
				}
				if p.openURL == "" {
					p.openURL = gif
				}
				return p, true
			}
		}
	}
	p.video = e.Type == discord.VideoEmbed || e.Video != nil && e.Type != discord.ImageEmbed
	if !p.video && e.Type != discord.ImageEmbed && e.Type != discord.GIFVEmbed {
		return mediaPreview{}, false
	}
	switch {
	case e.Image != nil && (e.Image.URL != "" || e.Image.Proxy != ""):
		p.proxy, p.original = string(e.Image.Proxy), string(e.Image.URL)
		p.width, p.height = int(e.Image.Width), int(e.Image.Height)
	case e.Thumbnail != nil && (e.Thumbnail.URL != "" || e.Thumbnail.Proxy != ""):
		p.proxy, p.original = string(e.Thumbnail.Proxy), string(e.Thumbnail.URL)
		p.width, p.height = int(e.Thumbnail.Width), int(e.Thumbnail.Height)
	case p.video && e.Video != nil:
		p.proxy = videoPosterURL(string(e.Video.Proxy))
		p.width, p.height = int(e.Video.Width), int(e.Video.Height)
	case e.Type == discord.ImageEmbed:
		p.original = string(e.URL)
	}
	if p.openURL == "" {
		if p.video && e.Video != nil {
			p.openURL = string(e.Video.URL)
		} else {
			p.openURL = p.original
		}
	}
	return p, p.openURL != "" || p.original != "" || p.proxy != ""
}

func isVideo(a discord.Attachment) bool {
	if strings.HasPrefix(strings.ToLower(a.ContentType), "video/") {
		return true
	}
	switch strings.ToLower(path.Ext(a.Filename)) {
	case ".mp4", ".webm", ".mov", ".m4v", ".mkv", ".avi", ".ogv":
		return true
	}
	return false
}

// Ask Discord for a still, rather than downloading a video into an image
// decoder. Other hosts must supply a thumbnail in the embed.
func videoPosterURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !discordMediaHost(u.Hostname()) {
		return ""
	}
	if u.Hostname() != "media.discordapp.net" {
		u.Host = "media.discordapp.net"
	}
	q := u.Query()
	q.Set("format", "webp")
	u.RawQuery = q.Encode()
	return u.String()
}

func stickerMediaURL(s discord.StickerItem) string {
	if !s.ID.IsValid() {
		return ""
	}
	switch s.FormatType {
	case discord.StickerFormatPNG, discord.StickerFormatAPNG:
		return "https://cdn.discordapp.com/stickers/" + s.ID.String() + ".png"
	case stickerFormatGIF:
		return "https://media.discordapp.net/stickers/" + s.ID.String() + ".gif"
	}
	return ""
}

func (c *chatView) layoutSticker(gtx layout.Context, s discord.StickerItem) layout.Dimensions {
	raw := stickerMediaURL(s)
	if raw == "" {
		target := ""
		if s.ID.IsValid() && s.FormatType == discord.StickerFormatLottie {
			target = "https://cdn.discordapp.com/stickers/" + s.ID.String() + ".json"
		}
		return c.layoutMediaFallback(gtx, "Sticker: "+s.Name, "Preview unavailable", target)
	}
	d := min(gtx.Constraints.Max.X, gtx.Dp(160))
	source := mediaURL("", raw, d, d)
	img := c.u.images.frame(gtx, source, d)
	if c.u.images.failed(source, d) {
		return c.layoutMediaFallback(gtx, "Sticker: "+s.Name, "Could not load · click to open", raw)
	}
	cl := c.linkClick("sticker:" + s.ID.String())
	if cl.Clicked(gtx) {
		c.openViewer("", raw, 320, 320, raw, source, d)
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			gtx.Constraints = layout.Exact(image.Pt(d, d))
			if img == nil {
				return layout.Center.Layout(gtx, label(c.u.th, 12, colorMuted, "Loading sticker…").Layout)
			}
			return widget.Image{Src: *img, Fit: widget.Contain}.Layout(gtx)
		})
	})
}

func (c *chatView) layoutMediaFallback(gtx layout.Context, title, detail, target string) layout.Dimensions {
	cl := c.linkClick("media-fallback:" + target + title)
	if target != "" && cl.Clicked(gtx) {
		openLink(target)
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if target != "" {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(oneLine(label(c.u.th, 14, colorText, title)).Layout),
						layout.Rigid(label(c.u.th, 12, colorMuted, detail).Layout),
					)
				})
			})
		})
	})
}

func previewSize(gtx layout.Context, w, h int) image.Point {
	if w <= 0 || h <= 0 {
		w, h = 400, 225
	}
	maxW, maxH := min(gtx.Constraints.Max.X, gtx.Dp(400)), gtx.Dp(300)
	dw := min(maxW, gtx.Dp(unit.Dp(w)))
	dh := int(int64(dw) * int64(h) / int64(w))
	if dh > maxH {
		dw, dh = int(int64(dw)*int64(maxH)/int64(dh)), maxH
	}
	return image.Pt(max(dw, 1), max(dh, 1))
}

func (c *chatView) layoutVideo(gtx layout.Context, p mediaPreview) layout.Dimensions {
	size := previewSize(gtx, p.width, p.height)
	cl := c.linkClick("video:" + p.openURL)
	if p.openURL != "" && cl.Clicked(gtx) {
		openLink(p.openURL)
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if p.openURL != "" {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			r := image.Rectangle{Max: size}
			defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
			paint.FillShape(gtx.Ops, colorSidebar, clip.Rect(r).Op())
			source := mediaURL(p.proxy, p.original, size.X, size.Y)
			if img := c.u.images.get(source, max(size.X, size.Y)); img != nil {
				gtx.Constraints = layout.Exact(size)
				widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
			}
			gtx.Constraints = layout.Exact(size)
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorServers, 28, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, iconPlay, 32, colorHeader)
					})
				})
			})
			title := p.title
			if title == "" {
				title = "Video"
			}
			gtx.Constraints = layout.Exact(size)
			layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorServers, 0, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(6)).Layout(gtx,
						oneLine(label(c.u.th, 12, colorHeader, title+" · click to open")).Layout)
				})
			})
			return layout.Dimensions{Size: size}
		})
	})
}
