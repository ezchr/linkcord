package gui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// paneLayout is where the user put each bar of the window: the server list
// left, right, top or bottom; the channels left or right; the members right
// or left; the user panel at the bottom or the top of the channels. Empty is
// the default, as on Discord.
type paneLayout struct {
	Servers   string `json:"servers,omitempty"`
	Channels  string `json:"channels,omitempty"`
	Members   string `json:"members,omitempty"`
	UserPanel string `json:"userPanel,omitempty"`
}

const (
	placeLeft   = "left"
	placeRight  = "right"
	placeTop    = "top"
	placeBottom = "bottom"
)

// normal is l with every place known, the default for those that are not.
func (l paneLayout) normal() paneLayout {
	pick := func(v, def string, ok ...string) string {
		for _, o := range ok {
			if v == o {
				return v
			}
		}
		return def
	}
	return paneLayout{
		Servers:   pick(l.Servers, placeLeft, placeLeft, placeRight, placeTop, placeBottom),
		Channels:  pick(l.Channels, placeLeft, placeLeft, placeRight),
		Members:   pick(l.Members, placeRight, placeLeft, placeRight),
		UserPanel: pick(l.UserPanel, placeBottom, placeBottom, placeTop),
	}
}

// saved is l as kept in the settings: the default places left out.
func (l paneLayout) saved() paneLayout {
	l = l.normal()
	def := paneLayout{}.normal()
	if l.Servers == def.Servers {
		l.Servers = ""
	}
	if l.Channels == def.Channels {
		l.Channels = ""
	}
	if l.Members == def.Members {
		l.Members = ""
	}
	if l.UserPanel == def.UserPanel {
		l.UserPanel = ""
	}
	return l
}

// serversAcross reports whether the server list is a strip across the
// window, at its top or bottom.
func (l paneLayout) serversAcross() bool {
	l = l.normal()
	return l.Servers == placeTop || l.Servers == placeBottom
}

type paneKind int

const (
	paneServers paneKind = iota
	paneChannels
	paneChat
	// paneSide is the member list, or the results of a search in its place.
	paneSide
)

// columnOrder is the columns from left to right. The server list is the
// outermost on its side, then the channels, then the members, by the chat.
// side is whether the member list shows.
func columnOrder(l paneLayout, side bool) []paneKind {
	l = l.normal()
	var left, right []paneKind
	add := func(k paneKind, place string) {
		if place == placeLeft {
			left = append(left, k)
		} else {
			// The right side is listed from the chat outwards, and turned.
			right = append([]paneKind{k}, right...)
		}
	}
	if !l.serversAcross() {
		add(paneServers, l.Servers)
	}
	add(paneChannels, l.Channels)
	if side {
		add(paneSide, l.Members)
	}
	out := append(left, paneChat)
	return append(out, right...)
}

// paneRects are where each column is in the window. A column not shown is
// empty.
type paneRects struct {
	servers, channels, chat, side image.Rectangle
}

// placePanes lays the columns out in area: the server list serverW wide, or
// tall as a strip across, the channels channelW and the side sideW wide, the
// chat the rest.
func placePanes(l paneLayout, area image.Rectangle, serverW, channelW, sideW int, side bool) paneRects {
	l = l.normal()
	var r paneRects
	row := area
	if l.serversAcross() {
		h := min(serverW, area.Dy())
		if l.Servers == placeTop {
			r.servers = image.Rect(area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+h)
			row.Min.Y += h
		} else {
			r.servers = image.Rect(area.Min.X, area.Max.Y-h, area.Max.X, area.Max.Y)
			row.Max.Y -= h
		}
	}
	widths := map[paneKind]int{paneServers: serverW, paneChannels: channelW, paneSide: sideW}
	order := columnOrder(l, side)
	fixed := 0
	for _, k := range order {
		fixed += widths[k]
	}
	widths[paneChat] = max(row.Dx()-fixed, 0)
	x := row.Min.X
	for _, k := range order {
		w := min(widths[k], max(row.Max.X-x, 0))
		rect := image.Rect(x, row.Min.Y, x+w, row.Max.Y)
		switch k {
		case paneServers:
			r.servers = rect
		case paneChannels:
			r.channels = rect
		case paneChat:
			r.chat = rect
		case paneSide:
			r.side = rect
		}
		x += w
	}
	return r
}

// layout returns the bars' places the user chose.
func (c *chatView) paneLayout() paneLayout { return c.u.appearance.Layout.normal() }

// serversAcross reports whether the server list is a strip across.
func (c *chatView) serversAcross() bool { return c.u.appearance.Layout.serversAcross() }

// across turns r, a rectangle of the server column, into the strip across:
// what is down the column goes along the strip.
func (c *chatView) across(r image.Rectangle) image.Rectangle {
	if !c.serversAcross() {
		return r
	}
	return image.Rect(r.Min.Y, r.Min.X, r.Max.Y, r.Max.X)
}

// along is the coordinate of p along the server list: down the column, or
// along the strip.
func (c *chatView) along(p f32.Point) float32 {
	if c.serversAcross() {
		return p.X
	}
	return p.Y
}

// updateGuildWheel scrolls the server strip with the wheel turned up and
// down: Gio scrolls a list across only with a sideways wheel, or with
// Shift. It takes what the list leaves, so it goes before the list.
func (c *chatView) updateGuildWheel(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.guildWheel, Kinds: pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Scroll {
			c.guildList.Position.Offset += int(e.Scroll.Y)
			c.guildList.Position.BeforeEnd = true
		}
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &c.guildWheel)
}

// layoutPane lays out w in the rectangle r of the window, filled with bg.
func layoutPane(gtx layout.Context, r image.Rectangle, bg colorT, w layout.Widget) {
	if r.Empty() {
		return
	}
	defer op.Offset(r.Min).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(r.Size())
	pane(gtx, bg, r.Dx(), w)
}

// userPanelTop reports whether the user panel is at the top of the
// channels.
func (c *chatView) userPanelTop() bool { return c.paneLayout().UserPanel == placeTop }

// voiceLogAt places the voice history by the user panel: above it, or
// below it when the panel is at the top. It returns the corner it hangs
// from, and whether that is its top.
func (c *chatView) voiceLogAt(gtx layout.Context) (image.Point, bool) {
	ch := c.channelsRect(gtx)
	x := ch.Min.X + gtx.Dp(8)
	if c.userPanelTop() {
		return image.Pt(x, ch.Min.Y+gtx.Dp(48)+c.voiceLog.panelH+gtx.Dp(6)), true
	}
	return image.Pt(x, ch.Max.Y-c.voiceLog.panelH-gtx.Dp(6)), false
}

// selfCardAt is where the user's own profile card goes, w by h: by the user
// panel, inside the window.
func (c *chatView) selfCardAt(gtx layout.Context, w, h int) image.Point {
	ch := c.channelsRect(gtx)
	size := gtx.Constraints.Max
	x := ch.Min.X + gtx.Dp(8)
	if c.paneLayout().Channels == placeRight {
		x = ch.Max.X - w - gtx.Dp(8)
	}
	x = min(max(x, gtx.Dp(8)), max(size.X-w-gtx.Dp(8), 0))
	if c.userPanelTop() {
		y := ch.Min.Y + gtx.Dp(48) + gtx.Dp(60)
		return image.Pt(x, max(min(y, size.Y-h-gtx.Dp(8)), 0))
	}
	return image.Pt(x, max(ch.Max.Y-gtx.Dp(60)-h, 0))
}

// channelsRect is where the channels were laid out, or the whole window
// before they were.
func (c *chatView) channelsRect(gtx layout.Context) image.Rectangle {
	if c.cols.channels.Empty() {
		return image.Rectangle{Max: gtx.Constraints.Max}
	}
	return c.cols.channels
}

// drawBelow is drawAbove with p the top left corner of w.
func drawBelow(gtx, wgtx layout.Context, p image.Point, tag event.Tag, w layout.Widget) {
	macro := op.Record(gtx.Ops)
	dims := w(wgtx)
	call := macro.Stop()
	t := op.Offset(p).Push(gtx.Ops)
	solid(gtx, tag, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// layoutColumns lays out the server list, the channels, the chat and the
// members where the user put them, over the background picture if there is
// one, with the bar of a voice channel lost across the top.
func (c *chatView) layoutColumns(gtx layout.Context) {
	l := c.paneLayout()
	c.pictureShown = c.u.layoutBackground(gtx)
	size := gtx.Constraints.Max
	area := image.Rectangle{Max: size}
	if c.lostVoice.ch != nil {
		// The bar of a voice channel lost goes over the columns.
		lgtx := gtx
		lgtx.Constraints.Min = image.Pt(size.X, 0)
		area.Min.Y = min(c.layoutLostVoice(lgtx).Size.Y, size.Y)
	}
	serverW := min(gtx.Dp(72), size.X)
	besides := serverW
	if l.serversAcross() {
		serverW = min(gtx.Dp(72), area.Dy())
		besides = 0
	}
	channels, members, showMembers := c.paneWidths(gtx, size.X, besides)
	// The results of a search take the members' place.
	searching := c.search.open && c.selChannel != nil
	sideW := members
	if searching {
		showMembers = false
		sideW = min(gtx.Dp(420), max(size.X-besides-channels-gtx.Dp(320), gtx.Dp(260)))
	}
	r := placePanes(l, area, serverW, channels, sideW, searching || showMembers)
	c.cols = r
	c.comp.paneX, c.comp.paneW, c.comp.paneBottom = r.chat.Min.X, r.chat.Dx(), r.chat.Max.Y

	layoutPane(gtx, r.servers, c.paneFill(colorServers, paneAlphaServers), c.layoutGuilds)
	layoutPane(gtx, r.channels, c.paneFill(colorSidebar, paneAlphaSidebar), c.layoutChannels)
	layoutPane(gtx, r.chat, c.paneFill(colorChat, paneAlphaChat), c.layoutMessages)
	switch {
	case searching:
		layoutPane(gtx, r.side, c.paneFill(colorSidebar, paneAlphaSidebar), c.layoutSearchResults)
	case showMembers:
		layoutPane(gtx, r.side, c.paneFill(colorSidebar, paneAlphaSidebar), c.layoutMembers)
	}

	// The edges to drag are between each column and the chat; the width
	// is measured from the column's outer edge.
	row := r.chat
	hgtx := gtx
	hgtx.Constraints.Max.Y = row.Dy()
	t := op.Offset(image.Pt(0, row.Min.Y)).Push(gtx.Ops)
	if l.Channels == placeRight {
		c.channelResize.origin = float32(r.channels.Max.X)
		c.channelResize.layout(hgtx, r.channels.Min.X)
	} else {
		c.channelResize.origin = float32(r.channels.Min.X)
		c.channelResize.layout(hgtx, r.channels.Max.X)
	}
	if showMembers {
		if l.Members == placeLeft {
			c.memberResize.origin = float32(r.side.Min.X)
			c.memberResize.layout(hgtx, r.side.Max.X)
		} else {
			c.memberResize.origin = float32(r.side.Max.X)
			c.memberResize.layout(hgtx, r.side.Min.X)
		}
	}
	t.Pop()
}
