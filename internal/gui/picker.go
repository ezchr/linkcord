package gui

import (
	"image"
	"net/url"
	"slices"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// gifResult is a GIF of Discord's GIF search.
type gifResult struct {
	URL    string `json:"url"`
	GIF    string `json:"gif_src"`
	Video  string `json:"src"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// gifPage is the GIFs tab: what was searched for and found.
type gifPage struct {
	list    widget.List
	clicks  []widget.Clickable
	results []gifResult
	// loaded is the search the results are of; typed is the one typed,
	// searched for once it stays the same a moment.
	loaded, typed string
	typedAt       time.Time
	loading       bool
	started       bool
	err           string
}

// stickerPage is the stickers tab.
type stickerPage struct {
	list   widget.List
	clicks map[discord.StickerID]*widget.Clickable
}

// guildStickers are the stickers of the servers, fetched once a session.
type guildStickers struct {
	byGuild  map[discord.GuildID][]discord.StickerItem
	fetching bool
}

func (c *chatView) layoutEmojiPicker(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	p := c.comp.emoji
	if !p.focused {
		gtx.Execute(key.FocusCmd{Tag: &p.search})
		p.focused = true
	}
	for {
		if _, ok := p.search.Update(gtx); !ok {
			break
		}
	}
	// Escape closes the picker.
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.search, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.comp.emoji = nil
			gtxFocus(c)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
	}
	// Reactions are emoji only, as on Discord.
	if p.react.IsValid() {
		p.tab = tabEmoji
	}
	for i := range p.tabs {
		if p.tabs[i].Clicked(gtx) {
			p.tab = i
			gtx.Execute(key.FocusCmd{Tag: &p.search})
		}
	}

	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, colorPanel, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Op(gtx.Ops))

	hint := map[int]string{tabGIFs: "Search GIFs", tabStickers: "Find stickers", tabEmoji: "Find the perfect emoji"}[p.tab]
	var body layout.Widget
	var done bool
	switch p.tab {
	case tabGIFs:
		body, done = c.gifBody(gtx, p)
	case tabStickers:
		body, done = c.stickerBody(gtx, p)
	default:
		body, done = c.emojiBody(gtx, p)
	}
	if done {
		return layout.Dimensions{Size: size}
	}

	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		if !p.react.IsValid() {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					var tabs []layout.FlexChild
					for i, name := range []string{"GIFs", "Stickers", "Emoji"} {
						tabs = append(tabs, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return p.tabs[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									pointer.CursorPointer.Add(gtx.Ops)
									bg, fg := colorT{}, colorMuted
									if p.tab == i {
										bg, fg = colorSelected, colorHeader
									} else if p.tabs[i].Hovered() {
										fg = colorText
									}
									return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
										return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx,
											bold(label(th, 14, fg, name)).Layout)
									})
								})
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, tabs...)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout))
		}
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return textField(gtx, th, &p.search, hint)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, body))
		if p.tab == tabEmoji {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.layoutEmojiPreview(gtx, p)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// emojiBody is the emoji tab: the rail of servers and categories beside the
// emoji. done reports that a pick closed the picker.
func (c *chatView) emojiBody(gtx layout.Context, p *emojiPicker) (layout.Widget, bool) {
	th := c.u.th
	sections := c.emojiSections(p)
	cellPx := gtx.Dp(40)
	railW := gtx.Dp(44)
	perRow := max((gtx.Constraints.Max.X-gtx.Dp(16)-railW-gtx.Dp(8))/cellPx, 1)

	// Each section is a title and rows of cells, flattened for the list.
	type row struct {
		title string
		icon  string
		cells []*emojiCell
	}
	var rows []row
	for _, s := range sections {
		if s.title == p.jump {
			p.list.Position = layout.Position{First: len(rows)}
			p.jump = ""
		}
		rows = append(rows, row{title: s.title, icon: s.icon})
		for i := 0; i < len(s.cells); i += perRow {
			rows = append(rows, row{cells: s.cells[i:min(i+perRow, len(s.cells))]})
		}
	}
	p.hovered, p.hoverPic = "", ""
	for _, r := range rows {
		for _, cl := range r.cells {
			if cl.click.Clicked(gtx) && !cl.locked {
				if id := p.react; id.IsValid() {
					e := emojiOfCell(cl)
					c.u.post(func() { c.toggleReaction(id, e) })
				} else {
					c.insertText(cl.text)
				}
				c.comp.emoji = nil
				return nil, true
			}
			if cl.click.Hovered() {
				p.hovered = cl.name
				p.hoverPic = cl.url
				if cl.url == "" {
					p.hoverPic = twemojiURL(cl.text, 48)
				}
				if cl.locked {
					p.hovered = cl.name + " · Nitro is needed to use this here"
				}
			}
		}
	}
	for _, s := range sections {
		if clickOf(p.railClicks, s.title).Clicked(gtx) {
			p.jump = s.title
			gtx.Execute(op.InvalidateCmd{})
		}
	}

	grid := func(gtx layout.Context) layout.Dimensions {
		if len(rows) == 0 {
			return layout.Center.Layout(gtx, label(th, 14, colorMuted, "No matching emoji.").Layout)
		}
		return listLayout(gtx, th, &p.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
			r := rows[i]
			if r.title != "" {
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4), Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if r.icon == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return c.u.picture(gtx, r.icon, 16, colorSidebar, r.title, -1)
							})
						}),
						layout.Rigid(oneLine(bold(label(th, 12, colorMuted, strings.ToUpper(r.title)))).Layout))
				})
			}
			var cells []layout.FlexChild
			for _, cl := range r.cells {
				cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return cl.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if !cl.locked {
							pointer.CursorPointer.Add(gtx.Ops)
						}
						sz := image.Pt(cellPx, cellPx)
						if cl.click.Hovered() {
							paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(4)).Op(gtx.Ops))
						}
						gtx.Constraints = layout.Exact(sz)
						d := layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return c.layoutPickerEmoji(gtx, cl, 30)
						})
						if cl.locked {
							// Dimmed, as Discord shows the emoji Nitro unlocks.
							paint.FillShape(gtx.Ops, withAlpha(colorPanel, 0x90), clip.Rect{Max: sz}.Op())
						}
						return d
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, cells...)
		})
	}
	rail := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(railW, gtx.Constraints.Max.Y))
		paint.FillShape(gtx.Ops, colorSidebar, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, gtx.Dp(6)).Op(gtx.Ops))
		return p.rail.Layout(gtx, len(sections), func(gtx layout.Context, i int) layout.Dimensions {
			s := sections[i]
			cl := clickOf(p.railClicks, s.title)
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				sz := image.Pt(railW, gtx.Dp(40))
				if cl.Hovered() {
					paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(6)).Op(gtx.Ops))
				}
				gtx.Constraints = layout.Exact(sz)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if s.emoji != "" {
						return c.twemoji(gtx, twemojiURL(s.emoji, 24), 22)
					}
					return c.u.picture(gtx, s.icon, 30, colorServers, s.title, gtx.Dp(10))
				})
			})
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx,
			layout.Rigid(rail),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, grid))
	}, false
}

// layoutPickerEmoji draws the emoji of a cell px wide: the server emoji's
// picture, Twemoji's, or the font's if Twemoji has none.
func (c *chatView) layoutPickerEmoji(gtx layout.Context, cl *emojiCell, size unit.Dp) layout.Dimensions {
	if cl.url != "" {
		return c.u.picture(gtx, cl.url, size, colorPanel, cl.name, 0)
	}
	if u := twemojiURL(cl.text, int(size)); isEmoji(cl.text) && !c.u.images.failed(u, gtx.Dp(size)) {
		return c.twemoji(gtx, u, size)
	}
	l := label(c.u.th, unit.Sp(float32(size)*0.75), colorHeader, cl.text)
	l.Alignment = text.Middle
	l.MaxLines = 1
	return l.Layout(gtx)
}

// layoutEmojiPreview is the bar under the emoji: the one under the pointer,
// big, with its name.
func (c *chatView) layoutEmojiPreview(gtx layout.Context, p *emojiPicker) layout.Dimensions {
	h := gtx.Dp(40)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.hoverPic == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.twemoji(gtx, p.hoverPic, 28)
			})
		}),
		layout.Flexed(1, oneLine(bold(label(c.u.th, 14, colorHeader, p.hovered))).Layout))
}

// gifBody is the GIFs tab: Discord's trending GIFs, or those found.
func (c *chatView) gifBody(gtx layout.Context, p *emojiPicker) (layout.Widget, bool) {
	th := c.u.th
	g := &p.gifs
	q := strings.TrimSpace(p.search.Text())
	if q != g.typed {
		g.typed, g.typedAt = q, gtx.Now
	}
	// Searched a moment after the typing stops.
	wait := 400 * time.Millisecond
	if !g.started {
		wait = 0
	}
	if (q != g.loaded || !g.started) && !g.loading {
		if gtx.Now.Sub(g.typedAt) >= wait {
			g.started, g.loading, g.err = true, true, ""
			go c.fetchGIFs(p, q)
		} else {
			gtx.Execute(op.InvalidateCmd{At: g.typedAt.Add(wait)})
		}
	}
	for i := range g.clicks {
		if g.clicks[i].Clicked(gtx) && i < len(g.results) && c.selChannel != nil {
			id, link := c.selChannel.ID, g.results[i].URL
			go func() {
				if _, err := c.st.SendMessage(id, link); err != nil {
					c.u.post(func() { c.sendErr = "Could not send the GIF: " + err.Error() })
				}
			}()
			c.comp.emoji = nil
			return nil, true
		}
	}
	grid := func(gtx layout.Context) layout.Dimensions {
		if len(g.results) == 0 {
			msg := "Loading GIFs…"
			if g.err != "" {
				msg = g.err
			} else if !g.loading {
				msg = "No GIFs found."
			}
			return layout.Center.Layout(gtx, label(th, 14, colorMuted, msg).Layout)
		}
		gap := gtx.Dp(6)
		cellW := (gtx.Constraints.Max.X - gap - gtx.Dp(10)) / 2
		cellH := gtx.Dp(110)
		rowsN := (len(g.results) + 1) / 2
		return listLayout(gtx, th, &g.list, rowsN, func(gtx layout.Context, r int) layout.Dimensions {
			var cells []layout.FlexChild
			for i := r * 2; i < min(r*2+2, len(g.results)); i++ {
				res := g.results[i]
				cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return g.clicks[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							sz := image.Pt(cellW, cellH)
							defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(6)).Push(gtx.Ops).Pop()
							paint.FillShape(gtx.Ops, colorSidebar, clip.Rect{Max: sz}.Op())
							if img := c.u.images.frame(gtx, gifPreview(res), cellW); img != nil {
								gtx.Constraints = layout.Exact(sz)
								widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
							}
							if g.clicks[i].Hovered() {
								paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x20), clip.Rect{Max: sz}.Op())
							}
							return layout.Dimensions{Size: sz}
						})
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, cells...)
		})
	}
	return grid, false
}

// gifPreview is what the picker plays of a GIF: its MP4, made a GIF by the
// image cache, as Discord's GIF embeds are. Klipy, where Discord's GIFs come
// from now, gives the GIF itself as moving WebP, which cannot be decoded
// here.
func gifPreview(r gifResult) string {
	if r.Video != "" {
		return r.Video + "#" + gifVideoFragment
	}
	return r.GIF
}

// fetchGIFs asks Discord for the GIFs of the search q, or trending ones.
// Whatever provider is asked for, Discord gives Klipy's now.
func (c *chatView) fetchGIFs(p *emojiPicker, q string) {
	res, err := c.discordGIFs(q)
	c.u.post(func() {
		g := &p.gifs
		g.loading, g.loaded = false, q
		if err != nil {
			g.err = "Could not load GIFs: " + err.Error()
			g.results, g.clicks = nil, nil
			return
		}
		g.results = res
		g.clicks = make([]widget.Clickable, len(res))
		g.list.Position = layout.Position{}
	})
}

// discordGIFs are the GIFs of Discord's own search.
func (c *chatView) discordGIFs(q string) ([]gifResult, error) {
	v := url.Values{"provider": {"tenor"}, "locale": {"en-US"}, "media_format": {"mp4"}}
	endpoint := api.Endpoint + "gifs/trending-gifs?"
	if q != "" {
		v.Set("q", q)
		endpoint = api.Endpoint + "gifs/search?"
	}
	return c.st.Client.RequestJSON[[]gifResult]("GET", endpoint+v.Encode())
}

// stickerBody is the stickers tab: the stickers of each server, this one
// first.
func (c *chatView) stickerBody(gtx layout.Context, p *emojiPicker) (layout.Widget, bool) {
	th := c.u.th
	sp := &p.stickers
	if sp.clicks == nil {
		sp.clicks = make(map[discord.StickerID]*widget.Clickable)
	}
	var here discord.GuildID
	if ch := c.selChannel; ch != nil {
		here = ch.GuildID
	}
	guilds := slices.Clone(c.guildOrder)
	if here.IsValid() {
		guilds = append([]discord.GuildID{here}, slices.DeleteFunc(guilds, func(id discord.GuildID) bool { return id == here })...)
	}
	c.fetchStickers(guilds)

	q := strings.ToLower(strings.TrimSpace(p.search.Text()))
	type row struct {
		title    string
		stickers []discord.StickerItem
		locked   bool
	}
	cellPx := gtx.Dp(84)
	perRow := max((gtx.Constraints.Max.X-gtx.Dp(10))/cellPx, 1)
	var rows []row
	for _, gid := range guilds {
		var list []discord.StickerItem
		for _, s := range c.stickers.byGuild[gid] {
			if q == "" || strings.Contains(strings.ToLower(s.Name), q) {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			continue
		}
		title := "Server"
		if g, err := c.st.Cabinet.Guild(gid); err == nil {
			title = g.Name
		}
		locked := c.me.Nitro == discord.NoUserNitro && gid != here
		rows = append(rows, row{title: title})
		for i := 0; i < len(list); i += perRow {
			rows = append(rows, row{stickers: list[i:min(i+perRow, len(list))], locked: locked})
		}
	}
	for _, r := range rows {
		for _, s := range r.stickers {
			if clickOf(sp.clicks, s.ID).Clicked(gtx) && !r.locked && c.selChannel != nil {
				id, sid := c.selChannel.ID, s.ID
				go func() {
					body := map[string]any{"sticker_ids": []string{sid.String()}}
					if err := c.st.Client.FastRequest("POST", api.EndpointChannels+id.String()+"/messages", httputil.WithJSONBody(body)); err != nil {
						c.u.post(func() { c.sendErr = "Could not send the sticker: " + err.Error() })
					}
				}()
				c.comp.emoji = nil
				return nil, true
			}
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		if len(rows) == 0 {
			msg := "No stickers."
			if c.stickers.fetching {
				msg = "Loading stickers…"
			}
			return layout.Center.Layout(gtx, label(th, 14, colorMuted, msg).Layout)
		}
		return listLayout(gtx, th, &sp.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
			r := rows[i]
			if r.title != "" {
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4), Left: unit.Dp(4)}.Layout(gtx,
					oneLine(bold(label(th, 12, colorMuted, strings.ToUpper(r.title)))).Layout)
			}
			var cells []layout.FlexChild
			for _, s := range r.stickers {
				cl := clickOf(sp.clicks, s.ID)
				cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						sz := image.Pt(cellPx, cellPx)
						if !r.locked {
							pointer.CursorPointer.Add(gtx.Ops)
						}
						if cl.Hovered() {
							paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(6)).Op(gtx.Ops))
						}
						pad := gtx.Dp(6)
						in := sz.Sub(image.Pt(2*pad, 2*pad))
						if raw := stickerMediaURL(s); raw != "" {
							if img := c.u.images.frame(gtx, mediaURL("", raw, in.X, in.Y), in.X); img != nil {
								t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
								igtx := gtx
								igtx.Constraints = layout.Exact(in)
								widget.Image{Src: *img, Fit: widget.Contain}.Layout(igtx)
								t.Pop()
							}
						} else {
							igtx := gtx
							igtx.Constraints = layout.Exact(sz)
							layout.Center.Layout(igtx, label(th, 11, colorMuted, s.Name).Layout)
						}
						if r.locked {
							paint.FillShape(gtx.Ops, withAlpha(colorPanel, 0x90), clip.Rect{Max: sz}.Op())
						}
						return layout.Dimensions{Size: sz}
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, cells...)
		})
	}, false
}

// fetchStickers gets the stickers of the servers not yet known, one server
// after the other, to keep clear of Discord's rate limits.
func (c *chatView) fetchStickers(guilds []discord.GuildID) {
	if c.stickers.fetching {
		return
	}
	if c.stickers.byGuild == nil {
		c.stickers.byGuild = make(map[discord.GuildID][]discord.StickerItem)
	}
	var missing []discord.GuildID
	for _, g := range guilds {
		if _, ok := c.stickers.byGuild[g]; !ok {
			missing = append(missing, g)
		}
	}
	if len(missing) == 0 {
		return
	}
	c.stickers.fetching = true
	go func() {
		for _, g := range missing {
			list, err := c.st.Client.RequestJSON[[]discord.StickerItem]("GET", api.EndpointGuilds+g.String()+"/stickers")
			if err != nil {
				list = nil
			}
			c.u.post(func() { c.stickers.byGuild[g] = list })
		}
		c.u.post(func() { c.stickers.fetching = false })
	}()
}

// twemoji draws the picture at u size wide, leaving the room empty while it
// loads.
func (c *chatView) twemoji(gtx layout.Context, u string, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	if img := c.u.images.frame(gtx, u, px); img != nil {
		return widget.Image{Src: *img, Fit: widget.Contain}.Layout(gtx)
	}
	return layout.Dimensions{Size: gtx.Constraints.Max}
}
