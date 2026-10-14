package gui

import (
	"cmp"
	"image"
	"slices"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// channelDrag is a channel, category or person in voice being dragged in
// the channel list: channels and categories to put them in another place,
// people to move them to another voice channel, as on Discord.
type channelDrag struct {
	pressed  bool
	dragging bool
	from     int // the row pressed
	start    f32.Point
	at       f32.Point
	// target is the row under the pointer, and where is -1 above its
	// middle, 1 below.
	target, where int
}

// rowHeight is how tall row i of the channel list was last drawn.
func (c *chatView) rowHeight(gtx layout.Context, i int) int {
	if h, ok := c.rowHeights[i]; ok {
		return h
	}
	return gtx.Dp(34)
}

// rowTop is the top of row k in the list as scrolled.
func (c *chatView) rowTop(gtx layout.Context, k int) int {
	p := c.channelList.Position
	y := -p.Offset
	for j := p.First; j < k; j++ {
		y += c.rowHeight(gtx, j)
	}
	for j := p.First - 1; j >= k; j-- {
		y -= c.rowHeight(gtx, j)
	}
	return y
}

// rowAt is the row at y in the list and whether y is in its upper or lower
// half.
func (c *chatView) rowAt(gtx layout.Context, y float32) (int, int) {
	n := len(c.rows)
	if n == 0 {
		return -1, 0
	}
	p := c.channelList.Position
	top := float32(-p.Offset)
	if y < top {
		return p.First, -1
	}
	for k := p.First; k < n; k++ {
		h := float32(c.rowHeight(gtx, k))
		if y < top+h {
			if y-top < h/2 {
				return k, -1
			}
			return k, 1
		}
		top += h
	}
	return n - 1, 1
}

// draggable reports whether row r can be dragged by the user.
func (c *chatView) draggable(r channelRow) bool {
	if !r.ch.GuildID.IsValid() {
		return false
	}
	perms := c.guildPerms(r.ch.GuildID)
	switch r.kind {
	case rowText, rowVoice, rowCategory:
		return perms.Has(discord.PermissionManageChannels)
	case rowVoiceMember:
		return r.user == c.me.ID || perms.Has(discord.PermissionMoveMembers)
	}
	return false
}

// updateChannelDrag follows the dragging in the channel list. It runs
// before the rows read their clicks, so that a drop is not also a click.
func (c *chatView) updateChannelDrag(gtx layout.Context) {
	d := &c.chDrag
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.chDrag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary {
				continue
			}
			i, _ := c.rowAt(gtx, e.Position.Y)
			if i < 0 || i >= len(c.rows) || !c.draggable(c.rows[i]) {
				*d = channelDrag{}
				continue
			}
			*d = channelDrag{pressed: true, from: i, start: e.Position, at: e.Position, target: -1}
		case pointer.Drag:
			if !d.pressed {
				continue
			}
			d.at = e.Position
			if !d.dragging && abs32(e.Position.Y-d.start.Y) > float32(gtx.Dp(8)) {
				d.dragging = true
			}
			if d.dragging {
				d.target, d.where = c.rowAt(gtx, e.Position.Y)
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Release:
			if d.dragging && d.from < len(c.rows) && d.target >= 0 && d.target < len(c.rows) {
				c.dropChannelRow(c.rows[d.from], c.rows[d.target], d.where)
				// The release is no click.
				for _, r := range c.rows {
					c.click(discord.Snowflake(r.ch.ID)).Clicked(gtx)
					if r.kind == rowVoiceMember {
						clickOf(c.voiceClicks, r.user).Clicked(gtx)
					}
				}
			}
			*d = channelDrag{}
		case pointer.Cancel:
			*d = channelDrag{}
		}
	}
}

// channelDropArea takes the dragging over the whole list, and shows where
// what is dragged would land.
func (c *chatView) channelDropArea(gtx layout.Context) {
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &c.chDrag)
	pass.Pop()
	area.Pop()
	d := &c.chDrag
	if !d.dragging || d.target < 0 || d.target >= len(c.rows) || d.from >= len(c.rows) {
		return
	}
	th := c.u.th
	src, dst := c.rows[d.from], c.rows[d.target]
	w := gtx.Constraints.Max.X
	y := c.rowTop(gtx, d.target)
	h := c.rowHeight(gtx, d.target)
	if src.kind == rowVoiceMember {
		// A ring around the voice channel the person would go to.
		if dst.kind == rowVoice || dst.kind == rowVoiceMember {
			ch := dst.ch.ID
			k := d.target
			for k > 0 && c.rows[k].kind == rowVoiceMember && c.rows[k].ch.ID == ch {
				k--
			}
			ry := c.rowTop(gtx, k)
			r := image.Rect(gtx.Dp(8), ry+gtx.Dp(2), w-gtx.Dp(8), ry+c.rowHeight(gtx, k))
			paint.FillShape(gtx.Ops, colorGreen, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(4)).Path(gtx.Ops), Width: float32(gtx.Dp(2))}.Op())
		}
	} else {
		ly := y + gtx.Dp(2)
		if d.where > 0 {
			ly = y + h
		}
		paint.FillShape(gtx.Ops, colorGreen, clip.UniformRRect(image.Rect(gtx.Dp(8), ly-gtx.Dp(1), w-gtx.Dp(8), ly+gtx.Dp(1)), gtx.Dp(1)).Op(gtx.Ops))
	}
	// What is dragged, under the pointer.
	m := op.Record(gtx.Ops)
	lgtx := gtx
	lgtx.Constraints = layout.Constraints{Max: image.Pt(w-gtx.Dp(16), gtx.Dp(40))}
	dims := rounded(lgtx, colorMenu, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(5), Bottom: unit.Dp(5)}.Layout(gtx, oneLine(label(th, 14, colorHeader, src.label)).Layout)
	})
	call := m.Stop()
	t := op.Offset(image.Pt(gtx.Dp(16), int(d.at.Y)-dims.Size.Y/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// dropChannelRow puts what row src is where it was dropped on row dst.
func (c *chatView) dropChannelRow(src, dst channelRow, where int) {
	switch src.kind {
	case rowVoiceMember:
		if dst.kind != rowVoice && dst.kind != rowVoiceMember || dst.ch.ID == src.ch.ID {
			return
		}
		if src.user == c.me.ID {
			c.joinVoice(dst.ch)
			return
		}
		c.patchMember("Could not move them", src.ch.GuildID, src.user, map[string]any{"channel_id": dst.ch.ID.String()})
	case rowCategory:
		c.moveCategory(src.ch, dst, where)
	case rowText, rowVoice:
		c.moveChannelTo(src.ch, dst, where)
	}
}

// channelPos is a channel's place, as Discord takes it.
type channelPos struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	ParentID any    `json:"parent_id,omitempty"`
	Lock     *bool  `json:"lock_permissions,omitempty"`
}

// reorder puts the channels in the order given, and sends it to Discord.
func (c *chatView) reorder(guildID discord.GuildID, order []discord.Channel, moved discord.ChannelID, parent discord.ChannelID) {
	var body []channelPos
	for i, ch := range order {
		pos := channelPos{ID: ch.ID.String(), Position: i}
		ch.Position = i
		if ch.ID == moved {
			pos.ParentID = nullID(discord.Snowflake(parent))
			no := false
			pos.Lock = &no
			ch.ParentID = parent
		}
		// Shown at once.
		c.st.Cabinet.ChannelSet(&ch, true)
		body = append(body, pos)
	}
	c.rebuildRows()
	if c.demo {
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointGuilds+guildID.String()+"/channels", httputil.WithJSONBody(body))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not move the channel", err) })
		}
	}()
}

// moveChannelTo puts the channel above or below the row it was dropped on,
// in that row's category, or at the top of a category dropped on.
func (c *chatView) moveChannelTo(ch discord.Channel, dst channelRow, where int) {
	parent := dst.ch.ParentID
	if dst.kind == rowCategory {
		parent = dst.ch.ID
	}
	voice := isVoice(ch.Type)
	// Its siblings, of its kind, as the list shows them.
	channels, _ := c.st.Cabinet.Channels(ch.GuildID)
	var siblings []discord.Channel
	for _, other := range channels {
		if other.ParentID == parent && other.ID != ch.ID && other.Type != discord.GuildCategory &&
			(isTextual(other.Type) || isVoice(other.Type)) && isVoice(other.Type) == voice {
			siblings = append(siblings, other)
		}
	}
	slices.SortFunc(siblings, func(a, b discord.Channel) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})
	at := 0
	switch {
	case dst.kind == rowCategory:
	case dst.ch.ID == ch.ID:
		return
	default:
		target := dst.ch.ID
		i := slices.IndexFunc(siblings, func(s discord.Channel) bool { return s.ID == target })
		switch {
		case i < 0 && voice:
			// A voice channel dropped among text channels goes first of
			// the voice channels, and a text one among voice, last.
			at = 0
		case i < 0:
			at = len(siblings)
		case where > 0:
			at = i + 1
		default:
			at = i
		}
	}
	order := slices.Insert(siblings, min(at, len(siblings)), ch)
	c.reorder(ch.GuildID, order, ch.ID, parent)
}

// moveCategory puts the category above or below the one it was dropped on.
func (c *chatView) moveCategory(cat discord.Channel, dst channelRow, where int) {
	target := dst.ch.ID
	if dst.kind != rowCategory {
		target = dst.ch.ParentID
	}
	cats := slices.DeleteFunc(c.guildCategories(cat.GuildID), func(o discord.Channel) bool { return o.ID == cat.ID })
	at := 0
	if i := slices.IndexFunc(cats, func(o discord.Channel) bool { return o.ID == target }); i >= 0 {
		at = i
		if where > 0 && dst.kind == rowCategory || dst.kind != rowCategory {
			at = i + 1
		}
	}
	if target == cat.ID {
		return
	}
	order := slices.Insert(cats, min(at, len(cats)), cat)
	var body []channelPos
	for i, ch := range order {
		ch.Position = i
		c.st.Cabinet.ChannelSet(&ch, true)
		body = append(body, channelPos{ID: ch.ID.String(), Position: i})
	}
	c.rebuildRows()
	if c.demo {
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointGuilds+cat.GuildID.String()+"/channels", httputil.WithJSONBody(body))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not move the category", err) })
		}
	}()
}
