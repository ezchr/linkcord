package gui

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/golive"
)

// streamPreview is the picture Discord keeps of a stream, refreshed every
// few minutes, as it last said.
type streamPreview struct {
	url    string
	asked  time.Time
	asking bool
}

// previewFor is the URL of the preview of the stream key, asked for again
// after a minute; "" until Discord gave one.
func (c *chatView) previewFor(key string) string {
	if c.previews == nil {
		c.previews = make(map[string]*streamPreview)
	}
	p := c.previews[key]
	if p == nil {
		p = new(streamPreview)
		c.previews[key] = p
	}
	every := time.Minute
	if c.u.images.lowPower {
		every = 5 * time.Minute
	}
	if !p.asking && time.Since(p.asked) > every && !c.demo {
		p.asking, p.asked = true, time.Now()
		go func() {
			res, err := c.st.Client.RequestJSON[struct {
				URL string `json:"url"`
			}]("GET", api.Endpoint+"streams/"+key+"/preview")
			c.u.post(func() {
				p.asking = false
				if err == nil {
					p.url = res.URL
				}
			})
		}()
	}
	return p.url
}

// streamKeyOf is the key of the stream of vs's user.
func (c *chatView) streamKeyOf(vs discord.VoiceState) string {
	guildID := vs.GuildID
	if ch, err := c.st.Cabinet.Channel(vs.ChannelID); err == nil {
		guildID = ch.GuildID
	}
	return golive.StreamKey(guildID, vs.ChannelID, vs.UserID)
}

// layoutPreviewPicture is a stream's preview, 16:9, w wide, or a dark box
// saying so while there is none.
func (c *chatView) layoutPreviewPicture(gtx layout.Context, key string, w int) layout.Dimensions {
	h := w * 9 / 16
	size := image.Pt(w, h)
	r := gtx.Dp(6)
	st := clip.UniformRRect(image.Rectangle{Max: size}, r).Push(gtx.Ops)
	paint.FillShape(gtx.Ops, colorServers, clip.Rect{Max: size}.Op())
	if url := c.previewFor(key); url != "" {
		if img := c.u.images.frame(gtx, url, w); img != nil {
			igtx := gtx
			igtx.Constraints = layout.Exact(size)
			widget.Image{Src: *img, Fit: widget.Cover}.Layout(igtx)
		}
	} else {
		lgtx := gtx
		lgtx.Constraints = layout.Exact(size)
		layout.Center.Layout(lgtx, label(c.u.th, 13, colorMuted, "No preview yet").Layout)
	}
	st.Pop()
	return layout.Dimensions{Size: size}
}

// liveHover is the stream whose preview shows beside the channel list,
// while the pointer is over its streamer or the card.
type liveHover struct {
	vs    discord.VoiceState
	y     int
	until time.Time
	watch widget.Clickable
	card  bool
}

// hoverLive shows the preview of r's stream while its row is hovered.
func (c *chatView) hoverLive(r channelRow, hovered bool) {
	if !r.live || !hovered {
		return
	}
	vs, err := c.st.Cabinet.VoiceState(r.ch.GuildID, r.user)
	if err != nil {
		return
	}
	h := &c.live
	if h.vs.UserID != vs.UserID {
		h.watch = widget.Clickable{}
	}
	h.vs, h.until = *vs, time.Now().Add(250*time.Millisecond)
	h.y = c.pointerAt.Y
}

// layoutLiveHover draws the card of the stream hovered, beside the
// channel list, as Discord's.
func (c *chatView) layoutLiveHover(gtx layout.Context) {
	h := &c.live
	if h.watch.Clicked(gtx) {
		vs := h.vs
		h.until = time.Time{}
		c.startWatching(vs)
		return
	}
	if !h.vs.UserID.IsValid() || time.Now().After(h.until) {
		return
	}
	gtx.Execute(op.InvalidateCmd{At: h.until})
	th := c.u.th
	w := gtx.Dp(300)
	x := c.comp.paneX + gtx.Dp(8)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	key := c.streamKeyOf(h.vs)
	game := ""
	if p := c.presence(h.vs.GuildID, h.vs.UserID); p != nil {
		for _, a := range p.Activities {
			if a.Type == discord.GameActivity {
				game = a.Name
			}
		}
	}
	joined := c.u.voiceChannel() == h.vs.ChannelID
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, bold(label(th, 13, colorText, "Streaming now")).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return liveBadge(gtx, th) }),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.layoutPreviewPicture(gtx, key, gtx.Constraints.Max.X)
				}),
			}
			if game != "" {
				rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout), layout.Rigid(bold(label(th, 15, colorHeader, game)).Layout))
			}
			if ids, ok := c.streamViewers(h.vs); ok && len(ids) > 0 {
				rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.layoutStreamViewers(gtx, h.vs.GuildID, ids)
				}))
			}
			if joined {
				rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return button(gtx, th, &h.watch, colorGreenButton, "Watch Stream")
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
	call := macro.Stop()
	y := min(max(h.y-dims.Size.Y/3, gtx.Dp(8)), gtx.Constraints.Max.Y-dims.Size.Y-gtx.Dp(8))
	t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	// Over the card, it stays.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &h.card, Kinds: pointer.Enter | pointer.Move | pointer.Leave})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind != pointer.Leave {
			h.until = time.Now().Add(250 * time.Millisecond)
		}
	}
	solid(gtx, h, dims)
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &h.card)
	pass.Pop()
	area.Pop()
	call.Add(gtx.Ops)
	t.Pop()
}
