package gui

import (
	"golang.org/x/exp/shiny/materialdesign/icons"
	"image"
	"log/slog"
	"math/rand/v2"
	"slices"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
)

var iconFolder = mustIcon(icons.FileFolder)

// serverFolder is a group of the server column, as Discord keeps them in
// the user's settings. A server outside any folder is a folder of its own
// with ID 0.
type serverFolder struct {
	ID     int64
	Name   string
	Color  uint32
	Guilds []discord.GuildID
}

// guildEntry is one row of the server column: a folder, or a server, which
// may be inside an open folder.
type guildEntry struct {
	folder *serverFolder
	guild  discord.GuildID
}

// guildDrag is a server or folder being dragged in the server column.
type guildDrag struct {
	pressed  bool
	dragging bool
	from     int // the entry pressed
	start    f32.Point
	at       f32.Point
	// target is the entry under the pointer and where on it: -1 before,
	// 0 on it (to make or join a folder), 1 after.
	target, where int
}

// foldersFromSettings makes the folders of the user's settings, with the
// servers they leave out on top, as on Discord.
func foldersFromSettings(folders []serverFolder, order []discord.GuildID) []serverFolder {
	seen := make(map[discord.GuildID]bool)
	var out []serverFolder
	for _, f := range folders {
		var guilds []discord.GuildID
		for _, g := range f.Guilds {
			if !seen[g] && slices.Contains(order, g) {
				seen[g] = true
				guilds = append(guilds, g)
			}
		}
		switch {
		case len(guilds) == 0:
		case f.ID == 0:
			for _, g := range guilds {
				out = append(out, serverFolder{Guilds: []discord.GuildID{g}})
			}
		default:
			f.Guilds = guilds
			out = append(out, f)
		}
	}
	var missing []serverFolder
	for _, g := range order {
		if !seen[g] {
			missing = append(missing, serverFolder{Guilds: []discord.GuildID{g}})
		}
	}
	return append(missing, out...)
}

// syncFolders makes the folders hold the servers of guildOrder, after
// servers were joined or left, and guildOrder their order.
func (c *chatView) syncFolders() {
	c.folders = foldersFromSettings(c.folders, c.guildOrder)
	c.guildOrder = c.guildOrder[:0]
	for _, f := range c.folders {
		c.guildOrder = append(c.guildOrder, f.Guilds...)
	}
}

// guildEntries are the rows of the server column after the direct
// messages.
func (c *chatView) guildEntries() []guildEntry {
	var out []guildEntry
	for i := range c.folders {
		f := &c.folders[i]
		if f.ID == 0 {
			out = append(out, guildEntry{guild: f.Guilds[0]})
			continue
		}
		out = append(out, guildEntry{folder: f})
		if c.openFolders[f.ID] {
			for _, g := range f.Guilds {
				out = append(out, guildEntry{folder: f, guild: g})
			}
		}
	}
	return out
}

func (c *chatView) folderColor(f *serverFolder) colorT {
	if f.Color == 0 {
		return colorAccent
	}
	return rgb(f.Color)
}

// layoutFolder is a folder's button: closed, its first four servers small;
// open, a folder in its color.
func (c *chatView) layoutFolder(gtx layout.Context, f *serverFolder) layout.Dimensions {
	click := clickOf(c.folderClicks, f.ID)
	open := c.openFolders[f.ID]
	unread := ningen.ChannelRead
	mentions := 0
	for _, g := range f.Guilds {
		if u := c.guildUnread(g); u > unread {
			unread = u
		}
		mentions += c.guildMentions(g)
	}
	if open {
		unread, mentions = ningen.ChannelRead, 0
	}
	col := c.folderColor(f)
	return c.guildButton(gtx, click, false, unread, mentions, nil, func(gtx layout.Context, active bool) layout.Dimensions {
		size := gtx.Dp(48)
		r := image.Rectangle{Max: image.Pt(size, size)}
		if open {
			paint.FillShape(gtx.Ops, withAlpha(col, 0x50), clip.UniformRRect(r, gtx.Dp(16)).Op(gtx.Ops))
			t := op.Offset(image.Pt((size-gtx.Dp(24))/2, (size-gtx.Dp(24))/2)).Push(gtx.Ops)
			icon(gtx, iconFolder, 24, col)
			t.Pop()
			return layout.Dimensions{Size: r.Max}
		}
		paint.FillShape(gtx.Ops, withAlpha(col, 0x40), clip.UniformRRect(r, gtx.Dp(16)).Op(gtx.Ops))
		mini := gtx.Dp(16)
		pad := (size - 2*mini - gtx.Dp(4)) / 2
		for i, g := range f.Guilds[:min(4, len(f.Guilds))] {
			x, y := pad+(i%2)*(mini+gtx.Dp(4)), pad+(i/2)*(mini+gtx.Dp(4))
			t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
			if guild, err := c.st.Cabinet.Guild(g); err == nil {
				c.u.picture(gtx, cdnSize(guild.IconURLWithType(discord.PNGImage)), 16, colorSidebar, guild.Name, gtx.Dp(5))
			}
			t.Pop()
		}
		return layout.Dimensions{Size: r.Max}
	})
}

// layoutGuildEntry lays out a row of the server column.
func (c *chatView) layoutGuildEntry(gtx layout.Context, e guildEntry) layout.Dimensions {
	if e.guild == 0 {
		return c.layoutFolder(gtx, e.folder)
	}
	id := e.guild
	guild, err := c.st.Cabinet.Guild(id)
	if err != nil {
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(56))}
	}
	if e.folder != nil {
		// The servers of an open folder lie on its color.
		r := image.Rect(gtx.Dp(12)-gtx.Dp(2), 0, gtx.Dp(12)+gtx.Dp(50), gtx.Dp(56)+gtx.Dp(4))
		paint.FillShape(gtx.Ops, withAlpha(c.folderColor(e.folder), 0x28), clip.Rect(r).Op())
	}
	return c.guildButton(gtx, c.click(discord.Snowflake(id)), id == c.selGuild, c.guildUnread(id), c.guildMentions(id), c.voiceBadge(id), func(gtx layout.Context, active bool) layout.Dimensions {
		// Servers are rounded squares, as on Discord.
		bg := colorSidebar
		if active {
			bg = colorAccent
		}
		return c.u.picture(gtx, cdnSize(guild.IconURLWithType(discord.PNGImage)), 48, bg, guild.Name, gtx.Dp(16))
	})
}

// guildRowH is the height of each row of the server column.
func guildRowH(gtx layout.Context) int { return gtx.Dp(56) }

// guildDropArea takes the dragging of servers over the whole column, whose
// first head rows are the direct messages.
func (c *chatView) guildDropArea(gtx layout.Context, head int) {
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &c.drag)
	pass.Pop()
	area.Pop()
	c.dragHead = head
	d := &c.drag
	if !d.dragging {
		return
	}
	// Where it would go: a line between rows, or a ring around the row it
	// would join.
	entries := c.guildEntries()
	rowH := guildRowH(gtx)
	y := c.entryY(gtx, d.target)
	switch {
	case d.target < 0 || d.target >= len(entries):
	case d.where == 0:
		r := image.Rect(gtx.Dp(12), y+gtx.Dp(8)-gtx.Dp(2), gtx.Dp(12)+gtx.Dp(52), y+rowH+gtx.Dp(2))
		paint.FillShape(gtx.Ops, withAlpha(colorAccent, 0x60), clip.UniformRRect(r, gtx.Dp(18)).Op(gtx.Ops))
	default:
		ly := y + gtx.Dp(4)
		if d.where > 0 {
			ly = y + rowH + gtx.Dp(4)
		}
		paint.FillShape(gtx.Ops, colorGreen, clip.UniformRRect(image.Rect(gtx.Dp(14), ly-gtx.Dp(2), gtx.Dp(58), ly+gtx.Dp(2)), gtx.Dp(2)).Op(gtx.Ops))
	}
}

// itemTop is the top of row k of the column, unscrolled: the last of the
// direct messages is taller by the line under it.
func (c *chatView) itemTop(gtx layout.Context, k int) int {
	y := k * guildRowH(gtx)
	if k >= c.dragHead {
		y += gtx.Dp(10)
	}
	return y
}

// entryY is the top of entry i in the column, after the scroll.
func (c *chatView) entryY(gtx layout.Context, i int) int {
	return c.itemTop(gtx, c.dragHead+i) - c.guildScroll(gtx)
}

// guildScroll is how far the column is scrolled, in pixels.
func (c *chatView) guildScroll(gtx layout.Context) int {
	p := c.guildList.Position
	return c.itemTop(gtx, p.First) + p.Offset
}

// entryAt is the entry at y in the column and where on it.
func (c *chatView) entryAt(gtx layout.Context, y float32, n int) (int, int) {
	rowH := float32(guildRowH(gtx))
	pos := y - float32(c.itemTop(gtx, c.dragHead)) + float32(c.guildScroll(gtx))
	i := int(pos / rowH)
	if pos < 0 {
		return 0, -1
	}
	if i >= n {
		return n - 1, 1
	}
	frac := pos/rowH - float32(i)
	switch {
	case frac < 0.3:
		return i, -1
	case frac > 0.7:
		return i, 1
	}
	return i, 0
}

// updateGuildDrag follows the dragging of servers. It runs before the
// clicks are read, so that a drop is not also a click.
func (c *chatView) updateGuildDrag(gtx layout.Context) {
	d := &c.drag
	entries := c.guildEntries()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.drag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
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
			i, _ := c.entryAt(gtx, e.Position.Y, len(entries))
			if e.Position.Y < float32(c.itemTop(gtx, c.dragHead)-c.guildScroll(gtx)) || i < 0 || i >= len(entries) {
				continue
			}
			*d = guildDrag{pressed: true, from: i, start: e.Position, at: e.Position, target: -1}
		case pointer.Drag:
			if !d.pressed {
				continue
			}
			d.at = e.Position
			if !d.dragging && abs32(e.Position.Y-d.start.Y) > float32(gtx.Dp(6)) {
				d.dragging = true
			}
			if d.dragging {
				d.target, d.where = c.entryAt(gtx, e.Position.Y, len(entries))
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Release:
			if d.dragging && d.from < len(entries) && d.target >= 0 && d.target < len(entries) {
				c.dropGuild(entries, d.from, d.target, d.where)
				// The release is no click.
				for _, e := range entries {
					if e.guild != 0 {
						c.click(discord.Snowflake(e.guild)).Clicked(gtx)
					} else {
						clickOf(c.folderClicks, e.folder.ID).Clicked(gtx)
					}
				}
			}
			*d = guildDrag{}
		case pointer.Cancel:
			*d = guildDrag{}
		}
	}
}

// dropGuild moves the entry from onto to: before, after or into it.
func (c *chatView) dropGuild(entries []guildEntry, from, to, where int) {
	if from == to {
		return
	}
	src, dst := entries[from], entries[to]
	// What moves: a whole folder, or one server out of where it is.
	var moving serverFolder
	if src.guild == 0 {
		moving = *src.folder
		if dst.folder == src.folder {
			return
		}
	} else {
		moving = serverFolder{Guilds: []discord.GuildID{src.guild}}
	}
	removeGuild := func(g discord.GuildID) {
		for i := range c.folders {
			c.folders[i].Guilds = slices.DeleteFunc(c.folders[i].Guilds, func(x discord.GuildID) bool { return x == g })
		}
	}
	if src.guild == 0 {
		c.folders = slices.DeleteFunc(c.folders, func(f serverFolder) bool { return f.ID == moving.ID })
	} else {
		removeGuild(src.guild)
	}
	// A folder left with one server is no folder any more, as on Discord.
	var kept []serverFolder
	for _, f := range c.folders {
		switch {
		case len(f.Guilds) == 0:
		case f.ID != 0 && len(f.Guilds) == 1:
			kept = append(kept, serverFolder{Guilds: f.Guilds})
		default:
			kept = append(kept, f)
		}
	}
	c.folders = kept

	// Where it lands.
	idx := slices.IndexFunc(c.folders, func(f serverFolder) bool {
		if dst.guild == 0 {
			return f.ID == dst.folder.ID && f.ID != 0
		}
		return slices.Contains(f.Guilds, dst.guild)
	})
	if idx < 0 {
		c.folders = append(c.folders, moving)
		c.saveFolders()
		return
	}
	target := &c.folders[idx]
	inFolder := dst.guild != 0 && target.ID != 0
	switch {
	case where == 0 && src.guild != 0 && target.ID != 0:
		// Into the folder, or the open folder of the server dropped on.
		target.Guilds = append(target.Guilds, src.guild)
	case where == 0 && src.guild != 0:
		// On a server: both make a new folder.
		target.ID = rand.Int64N(1<<31) + 1
		target.Guilds = append(target.Guilds, src.guild)
		c.openFolders[target.ID] = false
	case inFolder && src.guild != 0:
		// Between the servers of an open folder.
		at := slices.Index(target.Guilds, dst.guild)
		if where > 0 {
			at++
		}
		target.Guilds = slices.Insert(target.Guilds, at, src.guild)
	default:
		at := idx
		if where > 0 {
			at++
		}
		c.folders = slices.Insert(c.folders, at, moving)
	}
	c.saveFolders()
}

// saveFolders makes guildOrder follow the folders and keeps them in the
// user's Discord settings, which every client of theirs shares.
func (c *chatView) saveFolders() {
	c.guildOrder = c.guildOrder[:0]
	for _, f := range c.folders {
		c.guildOrder = append(c.guildOrder, f.Guilds...)
	}
	if c.demo {
		return
	}
	type folder struct {
		ID       *int64            `json:"id"`
		Name     *string           `json:"name"`
		Color    *uint32           `json:"color"`
		GuildIDs []discord.GuildID `json:"guild_ids"`
	}
	var body []folder
	positions := slices.Clone(c.guildOrder)
	for _, f := range c.folders {
		out := folder{GuildIDs: f.Guilds}
		if f.ID != 0 {
			id := f.ID
			out.ID = &id
			if f.Name != "" {
				name := f.Name
				out.Name = &name
			}
			if f.Color != 0 {
				col := f.Color
				out.Color = &col
			}
		}
		body = append(body, out)
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointMe+"/settings",
			httputil.WithJSONBody(map[string]any{"guild_folders": body, "guild_positions": positions}))
		if err != nil {
			slog.Warn("could not save the server folders", "err", err)
			c.u.post(func() { c.showNotice("Could not save the server order") })
		}
	}()
}
