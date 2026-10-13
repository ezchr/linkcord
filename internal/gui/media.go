package gui

import (
	"image"
	"image/color"
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
	playURL                  string
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
	// Prefer a real GIF over a static thumbnail or a video rendition.
	if e.Type == discord.GIFVEmbed {
		for _, candidate := range []string{
			func() string {
				if e.Image != nil {
					return string(e.Image.URL)
				}
				return ""
			}(),
			func() string {
				if e.Thumbnail != nil {
					return string(e.Thumbnail.URL)
				}
				return ""
			}(),
			string(e.URL),
		} {
			if parsed, err := url.Parse(candidate); err == nil && strings.EqualFold(path.Ext(parsed.Path), ".gif") {
				p.original = candidate
				if e.Image != nil {
					p.width, p.height = int(e.Image.Width), int(e.Image.Height)
				} else if e.Video != nil {
					p.width, p.height = int(e.Video.Width), int(e.Video.Height)
				} else if e.Thumbnail != nil {
					p.width, p.height = int(e.Thumbnail.Width), int(e.Thumbnail.Height)
				}
				if p.openURL == "" {
					p.openURL = candidate
				}
				return p, true
			}
		}
		if e.Video != nil {
			source := string(e.Video.URL)
			if source == "" {
				source = string(e.Video.Proxy)
			}
			if parsed, err := url.Parse(source); err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
				parsed.Fragment = gifVideoFragment
				p.original = parsed.String()
				p.width, p.height = int(e.Video.Width), int(e.Video.Height)
				if p.openURL == "" {
					p.openURL = source
				}
				return p, true
			}
		}
	}
	p.video = e.Type == discord.VideoEmbed || e.Video != nil && e.Type != discord.ImageEmbed
	if p.video && e.Video != nil {
		p.playURL = string(e.Video.URL)
		if p.playURL == "" {
			p.playURL = string(e.Video.Proxy)
		}
	}
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
	title := p.title
	if title == "" {
		title = "Video"
	}
	// The video playing here, if it is this one and not in the fullscreen
	// layer, whose controls then belong to the layer.
	v := c.video
	if v != nil && v.source != p.playURL {
		v = nil
	}
	inTile := v != nil && !v.visible()
	cl := c.linkClick("video:" + p.openURL)
	if inTile {
		if v.tileVolume.Update(gtx) {
			c.setVolume(v, v.tileVolume.Value)
		}
		if v.tilePause.Clicked(gtx) {
			v.togglePause()
		}
		if v.tileFull.Clicked(gtx) {
			v.setShown(true)
		}
	}
	if cl.Clicked(gtx) {
		switch {
		case v == nil:
			c.openVideo(p.playURL, title, false)
			v = c.video
			inTile = true
		case c.videoLoading(p.playURL):
			// A click while it loads gives up on it.
			c.closeVideo()
			v, inTile = nil, false
		case v.isEnded():
			c.openVideo(p.playURL, title, false)
			v = c.video
		default:
			v.togglePause()
		}
	}
	var frame *paint.ImageOp
	status, ended := "", false
	if v != nil {
		v.mu.Lock()
		frame, status, ended = v.frame, v.status, v.ended
		v.mu.Unlock()
	}
	loading := v != nil && frame == nil && !ended
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		r := image.Rectangle{Max: size}
		cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if p.openURL != "" {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
			gtx.Constraints = layout.Exact(size)
			if inTile && frame != nil {
				paint.FillShape(gtx.Ops, color.NRGBA{A: 0xff}, clip.Rect(r).Op())
				widget.Image{Src: *frame, Fit: widget.Contain}.Layout(gtx)
			} else {
				paint.FillShape(gtx.Ops, colorSidebar, clip.Rect(r).Op())
				source := mediaURL(p.proxy, p.original, size.X, size.Y)
				if img := c.u.images.get(source, max(size.X, size.Y)); img != nil {
					widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
				}
			}
			// The play button, a spinner while loading, nothing while playing.
			playing := inTile && frame != nil && !ended && !v.paused.Load()
			if !playing {
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return rounded(gtx, colorServers, 28, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if loading {
								gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(32), gtx.Dp(32)))
								return loader(c.u.th).Layout(gtx)
							}
							return icon(gtx, iconPlay, 32, colorHeader)
						})
					})
				})
			}
			return layout.Dimensions{Size: size}
		})
		// The bar at the bottom: the title, or the controls once it plays.
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops).Pop()
		layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, withAlpha(colorServers, 0xe0), 0, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if inTile && (frame != nil || ended) {
						return c.layoutVideoControls(gtx, v, &v.tilePause, &v.tileFull, &v.tileVolume, "Fullscreen", status)
					}
					text := title + " · click to play"
					switch {
					case loading:
						text = title + " · loading…"
					case v != nil && v.visible():
						text = title + " · playing fullscreen"
					}
					return oneLine(label(c.u.th, 12, colorHeader, text)).Layout(gtx)
				})
			})
		})
		return layout.Dimensions{Size: size}
	})
}
