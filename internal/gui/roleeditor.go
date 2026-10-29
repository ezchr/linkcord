package gui

import (
	"encoding/base64"
	"fmt"
	"image"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ncruces/zenity"
)

// The tabs of the role editor, as Discord's, but its links.
const (
	roleDisplay = iota
	rolePermissions
	roleMembers
)

// roleEditor is a role being edited in the server's settings.
type roleEditor struct {
	id   discord.RoleID // zero: the list shows
	role discord.Role   // as last saved, changed here before Discord says
	tab  int
	tabs [3]widget.Clickable
	back widget.Clickable
	more widget.Clickable

	// Display.
	name                 widget.Editor
	origName             string
	color, origColor     discord.Color
	second, origSecond   discord.Color
	secondPresets        [20]widget.Clickable
	gradBtn              widget.Clickable
	emoji                widget.Editor
	origEmoji            string
	picker               colorPicker
	pickerOpen           bool
	defaultBtn, custom   widget.Clickable
	presets              [20]widget.Clickable
	iconBtn, iconRemove  widget.Clickable
	hoistBtn, mentionBtn widget.Clickable
	save, reset          widget.Clickable

	// Permissions.
	permSearch widget.Editor
	permBtns   map[uint]*widget.Clickable
	clearPerms widget.Clickable

	// Manage Members.
	memberSearch widget.Editor
	addMember    widget.Clickable
	removeMember map[discord.UserID]*widget.Clickable
}

// rolePresets are the colors Discord offers for roles.
var rolePresets = [20]uint32{
	0x1abc9c, 0x2ecc71, 0x3498db, 0x9b59b6, 0xe91e63, 0xf1c40f, 0xe67e22, 0xe74c3c, 0x95a5a6, 0x607d8b,
	0x11806a, 0x1f8b4c, 0x206694, 0x71368a, 0xad1457, 0xc27c0e, 0xa84300, 0x992d22, 0x979c9f, 0x546e7a,
}

// roleColorsBody is a role's colors for Discord: the color, and the color its
// name fades to, if it has one.
func roleColorsBody(primary, secondary discord.Color) map[string]any {
	body := map[string]any{"primary_color": uint32(primary), "secondary_color": nil}
	if secondary > 0 {
		body["secondary_color"] = uint32(secondary)
	}
	return body
}

// myTopRole is the position of the user's highest role in the server.
func (c *chatView) myTopRole(guildID discord.GuildID) int {
	m, err := c.st.Cabinet.Member(guildID, c.me.ID)
	if err != nil {
		return 0
	}
	top := 0
	for _, id := range m.RoleIDs {
		if r, err := c.st.Cabinet.Role(guildID, id); err == nil && r.Position > top {
			top = r.Position
		}
	}
	return top
}

// canEditRole reports whether the user may change the role: only those
// below their highest role, unless they own the server.
func (c *chatView) canEditRole(guildID discord.GuildID, r discord.Role) bool {
	if c.demo {
		return true
	}
	g, err := c.st.Cabinet.Guild(guildID)
	if err == nil && g.OwnerID == c.me.ID {
		return true
	}
	return c.guildPerms(guildID).Has(discord.PermissionManageRoles) && r.Position < c.myTopRole(guildID)
}

// openRoleEditor shows the role's editor, on the tab given.
func (c *chatView) openRoleEditor(id discord.RoleID, tab int) {
	gs := &c.gset
	r, err := c.st.Cabinet.Role(gs.guildID, id)
	if err != nil {
		return
	}
	re := &gs.lists.role
	*re = roleEditor{id: id, role: *r, tab: tab, origName: r.Name, color: r.Color, origColor: r.Color, second: r.Colors.Secondary, origSecond: r.Colors.Secondary, origEmoji: r.UnicodeEmoji}
	re.name.SingleLine = true
	re.name.SetText(r.Name)
	re.emoji.SingleLine = true
	re.emoji.SetText(r.UnicodeEmoji)
	re.permSearch.SingleLine, re.memberSearch.SingleLine = true, true
	gs.list.Position = layout.Position{}
	gs.err, gs.info = "", ""
}

// createRole makes a role, as Discord names them, and opens its editor.
func (c *chatView) createRole() {
	gs := &c.gset
	guildID := gs.guildID
	if c.demo {
		r := discord.Role{ID: discord.RoleID(discord.NewSnowflake(time.Now())), Name: "new role", Position: 1}
		c.st.Cabinet.RoleSet(guildID, &r, false)
		c.openRoleEditor(r.ID, roleDisplay)
		return
	}
	go func() {
		r, err := c.st.Client.RequestJSON[discord.Role]("POST", api.EndpointGuilds+guildID.String()+"/roles", httputil.WithJSONBody(map[string]any{"name": "new role"}))
		c.u.post(func() {
			if err != nil {
				gs.err = "Discord did not make the role: " + err.Error()
				return
			}
			c.st.Cabinet.RoleSet(guildID, &r, false)
			c.openRoleEditor(r.ID, roleDisplay)
		})
	}()
}

// patchRole changes the role: apply shows it at once, body goes to
// Discord.
func (c *chatView) patchRole(apply func(*discord.Role), body map[string]any) {
	gs := &c.gset
	re := &gs.lists.role
	apply(&re.role)
	r := re.role
	c.st.Cabinet.RoleSet(gs.guildID, &r, true)
	c.guildRequest("PATCH", api.EndpointGuilds+gs.guildID.String()+"/roles/"+re.id.String(), body, nil)
}

func (c *chatView) updateRoleEditor(gtx layout.Context) {
	gs := &c.gset
	re := &gs.lists.role
	if re.id == 0 {
		return
	}
	if re.back.Clicked(gtx) {
		re.id = 0
		gs.list.Position = layout.Position{}
		return
	}
	for i := range re.tabs {
		if re.tabs[i].Clicked(gtx) {
			re.tab = i
		}
	}
	editable := c.canEditRole(gs.guildID, re.role)
	everyone := re.id == discord.RoleID(gs.guildID)
	if re.more.Clicked(gtx) {
		id := re.id
		c.openMenu(c.pointerAt, 200, func() []menuItem {
			items := []menuItem{{text: "Copy Role ID", act: func() { c.copyText = id.String() }}}
			if editable && !everyone && !re.role.Managed {
				items = append(items, menuItem{text: "Delete Role", col: colorRed, act: func() { c.askDeleteRole(id) }})
			}
			return items
		})
	}
	if !editable {
		return
	}

	// Display.
	if re.save.Clicked(gtx) {
		name := strings.TrimSpace(re.name.Text())
		body := map[string]any{}
		if name != re.origName && name != "" && !re.role.Managed {
			body["name"] = name
		}
		if re.color != re.origColor || re.second != re.origSecond {
			body["color"] = uint32(re.color)
			body["colors"] = roleColorsBody(re.color, re.second)
		}
		emoji := strings.TrimSpace(re.emoji.Text())
		if emoji != re.origEmoji {
			body["unicode_emoji"] = emoji
			if emoji == "" {
				body["unicode_emoji"] = nil
			} else {
				body["icon"] = nil
			}
		}
		if len(body) > 0 {
			col, second := re.color, re.second
			c.patchRole(func(r *discord.Role) {
				if n, ok := body["name"].(string); ok {
					r.Name = n
				}
				r.Color = col
				r.Colors = discord.RoleColors{Primary: col, Secondary: second}
				r.UnicodeEmoji = emoji
				if emoji != "" {
					r.Icon = ""
				}
			}, body)
			re.origName, re.origColor = cmpOr(name, re.origName), re.color
			re.origSecond, re.origEmoji = re.second, emoji
		}
	}
	if re.reset.Clicked(gtx) {
		re.name.SetText(re.origName)
		re.color = re.origColor
		re.second = re.origSecond
		re.emoji.SetText(re.origEmoji)
		re.pickerOpen = false
	}
	if re.defaultBtn.Clicked(gtx) {
		re.color, re.pickerOpen = 0, false
	}
	if re.custom.Clicked(gtx) {
		re.pickerOpen = !re.pickerOpen
		if re.color != 0 {
			re.picker.set(rgb(uint32(re.color)))
		}
	}
	if re.pickerOpen {
		if changed, _ := re.picker.update(gtx); changed {
			col := re.picker.color()
			re.color = discord.Color(uint32(col.R)<<16 | uint32(col.G)<<8 | uint32(col.B))
		}
	}
	for i := range re.presets {
		if re.presets[i].Clicked(gtx) {
			re.color, re.pickerOpen = discord.Color(rolePresets[i]), false
		}
	}
	if re.gradBtn.Clicked(gtx) {
		if re.second == 0 {
			re.second = discord.Color(rolePresets[3])
		} else {
			re.second = 0
		}
	}
	for i := range re.secondPresets {
		if re.secondPresets[i].Clicked(gtx) {
			re.second = discord.Color(rolePresets[i])
		}
	}
	if re.hoistBtn.Clicked(gtx) {
		on := !re.role.Hoist
		c.patchRole(func(r *discord.Role) { r.Hoist = on }, map[string]any{"hoist": on})
	}
	if re.mentionBtn.Clicked(gtx) {
		on := !re.role.Mentionable
		c.patchRole(func(r *discord.Role) { r.Mentionable = on }, map[string]any{"mentionable": on})
	}
	if re.iconBtn.Clicked(gtx) {
		c.pickRoleIcon()
	}
	if re.iconRemove.Clicked(gtx) {
		c.patchRole(func(r *discord.Role) { r.Icon = "" }, map[string]any{"icon": nil, "unicode_emoji": nil})
	}

	// Permissions.
	for bit, click := range re.permBtns {
		if click.Clicked(gtx) {
			p := re.role.Permissions ^ discord.Permissions(1)<<bit
			c.patchRole(func(r *discord.Role) { r.Permissions = p }, map[string]any{"permissions": strconv.FormatUint(uint64(p), 10)})
		}
	}
	if re.clearPerms.Clicked(gtx) {
		c.patchRole(func(r *discord.Role) { r.Permissions = 0 }, map[string]any{"permissions": "0"})
	}

	// Manage Members.
	if re.addMember.Clicked(gtx) && !re.role.Managed && !everyone {
		c.openMenu(c.pointerAt, 260, c.addRoleMemberItems)
	}
	for uid, click := range re.removeMember {
		if click.Clicked(gtx) {
			c.setMemberRole(uid, re.id, false)
		}
	}
}

// setMemberRole gives the member the role, or takes it away.
func (c *chatView) setMemberRole(uid discord.UserID, rid discord.RoleID, give bool) {
	gs := &c.gset
	if m, err := c.st.Cabinet.Member(gs.guildID, uid); err == nil {
		mm := *m
		mm.RoleIDs = slices.DeleteFunc(slices.Clone(mm.RoleIDs), func(id discord.RoleID) bool { return id == rid })
		if give {
			mm.RoleIDs = append(mm.RoleIDs, rid)
		}
		c.st.Cabinet.MemberSet(gs.guildID, &mm, true)
	}
	method := "DELETE"
	if give {
		method = "PUT"
	}
	c.guildRequest(method, api.EndpointGuilds+gs.guildID.String()+"/members/"+uid.String()+"/roles/"+rid.String(), nil, nil)
}

// addRoleMemberItems are the members without the role, as searched.
func (c *chatView) addRoleMemberItems() []menuItem {
	gs := &c.gset
	re := &gs.lists.role
	q := strings.ToLower(strings.TrimSpace(re.memberSearch.Text()))
	members, _ := c.st.Cabinet.Members(gs.guildID)
	var items []menuItem
	for _, m := range members {
		if slices.Contains(m.RoleIDs, re.id) || m.User.Bot {
			continue
		}
		name := c.memberName(gs.guildID, m.User.ID, &m)
		if q != "" && !strings.Contains(strings.ToLower(name+" "+m.User.Username), q) {
			continue
		}
		uid := m.User.ID
		items = append(items, menuItem{key: "add" + uid.String(), text: name, act: func() { c.setMemberRole(uid, re.id, true) }})
		if len(items) >= 30 {
			break
		}
	}
	if len(items) == 0 {
		items = append(items, menuItem{title: true, text: "No one to add"})
	}
	return items
}

// askDeleteRole asks whether to delete the role, and deletes it.
func (c *chatView) askDeleteRole(id discord.RoleID) {
	gs := &c.gset
	name := "the role"
	if r, err := c.st.Cabinet.Role(gs.guildID, id); err == nil {
		name = r.Name
	}
	c.prompt("Delete '"+name+"'", "Delete", true, []string{"Are you sure you want to delete " + name + "? This cannot be undone."}, nil, func([]string) {
		guildID := gs.guildID
		c.guildRequest("DELETE", api.EndpointGuilds+guildID.String()+"/roles/"+id.String(), nil, func() {
			c.st.Cabinet.RoleRemove(guildID, id)
			if gs.lists.role.id == id {
				gs.lists.role.id = 0
			}
		})
	})
}

// pickRoleIcon asks for a picture, of 256 KB at most, as the role's icon.
func (c *chatView) pickRoleIcon() {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		path, err := zenity.SelectFile(zenity.Title("Role icon"), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"}})
		if err != nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 256<<10 {
			c.u.post(func() { c.gset.err = "Role icons are 256 KB at most." })
			return
		}
		uri := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
		c.u.post(func() {
			c.patchRole(func(*discord.Role) {}, map[string]any{"icon": uri})
		})
	}()
}

// rolePermGroups are the permissions of a role, as Discord's settings list
// them.
func rolePermGroups() []struct {
	title string
	perms []channelPerm
} {
	groups := []struct {
		title string
		perms []channelPerm
	}{
		{"General Server Permissions", []channelPerm{
			{10, "View Channels", "Allows members to view channels by default (excluding private channels)."},
			{4, "Manage Channels", "Allows members to create, edit or delete channels."},
			{28, "Manage Roles", "Allows members to create new roles and edit or delete roles lower than their highest role. Also allows them to change permissions of individual channels they can access."},
			{43, "Create Expressions", "Allows members to add custom emoji, stickers and sounds in this server."},
			{30, "Manage Expressions", "Allows members to edit or remove custom emoji, stickers and sounds in this server."},
			{7, "View Audit Log", "Allows members to view a record of who made which changes in this server."},
			{19, "View Server Insights", "Allows members to view Server Insights, which shows data on community growth, engagement and more."},
			{29, "Manage Webhooks", "Allows members to create, edit or delete webhooks, which can post messages from other apps or sites into this server."},
			{5, "Manage Server", "Allows members to change this server's name, switch regions, view all invites, add apps to this server and create and update AutoMod rules."},
		}},
		{"Membership Permissions", []channelPerm{
			{0, "Create Invite", "Allows members to invite new people to this server."},
			{26, "Change Nickname", "Allows members to change their own nickname, a custom name for just this server."},
			{27, "Manage Nicknames", "Allows members to change the nicknames of other members."},
			{1, "Kick, Approve and Reject Members", "Allows members to remove other members from this server, and to approve or reject requests to join."},
			{2, "Ban Members", "Allows members to permanently ban and delete the message history of other members from this server."},
			{40, "Timeout Members", "When you put a user in timeout they will not be able to send messages, react, join voice or stage channels."},
		}},
	}
	for _, g := range channelPermGroups[2:] {
		groups = append(groups, struct {
			title string
			perms []channelPerm
		}{g.title, g.perms})
	}
	return append(groups,
		struct {
			title string
			perms []channelPerm
		}{"Stage Channel Permissions", []channelPerm{{32, "Request to Speak", "Allows requests to speak in Stage channels."}}},
		struct {
			title string
			perms []channelPerm
		}{"Advanced Permissions", []channelPerm{{3, "Administrator", "Members with this permission have every permission and can bypass channel-specific permissions or restrictions. This is a dangerous permission to grant."}}},
	)
}

// roleEditorRows are the role editor, in place of the list of roles.
func (c *chatView) roleEditorRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	re := &gs.lists.role
	r := re.role
	editable := c.canEditRole(gs.guildID, r)
	everyone := re.id == discord.RoleID(gs.guildID)
	members, _ := c.st.Cabinet.Members(gs.guildID)
	var inRole []discord.Member
	for _, m := range members {
		if slices.Contains(m.RoleIDs, re.id) {
			inRole = append(inRole, m)
		}
	}
	rows := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &re.back, iconBack, colorMuted, colorHeader)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, oneLine(bold(label(th, 15, colorHeader, "EDIT ROLE: "+strings.ToUpper(r.Name)))).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconButton(gtx, &re.more, iconMore, colorMuted, colorHeader)
			}),
		)
	}, layout.Spacer{Height: unit.Dp(12)}.Layout}
	if r.Managed {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, withAlpha(colorYellow, 0x20), 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(12)).Layout(gtx, label(th, 14, colorText, "⚠ This role is managed by an integration. It cannot be manually assigned to members.").Layout)
				})
			})
		})
	}
	if !editable {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorMuted, "🔒 You can only look at this role: it is not below your highest role.").Layout)
		})
	}
	// The tabs.
	names := []string{"Display", "Permissions", fmt.Sprintf("Manage Members (%d)", len(inRole))}
	if everyone {
		re.tab = rolePermissions
		names = []string{"", "Permissions", ""}
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		var tabs []layout.FlexChild
		for i, n := range names {
			if n == "" {
				continue
			}
			i, n := i, n
			tabs = append(tabs, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return re.tabs[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						col := colorMuted
						if re.tab == i || re.tabs[i].Hovered() {
							col = colorHeader
						}
						dims := layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, label(th, 15, col, n).Layout)
						if re.tab == i {
							paint.FillShape(gtx.Ops, colorAccent, clip.Rect{Min: image.Pt(0, dims.Size.Y-gtx.Dp(2)), Max: dims.Size}.Op())
						}
						return dims
					})
				})
			}))
		}
		return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, tabs...) })
	})
	switch re.tab {
	case roleDisplay:
		rows = append(rows, c.roleDisplayRows(editable)...)
	case rolePermissions:
		rows = append(rows, c.rolePermissionRows(editable)...)
	case roleMembers:
		rows = append(rows, c.roleMemberRows(inRole, editable)...)
	}
	return rows
}

func (c *chatView) roleDisplayRows(editable bool) []layout.Widget {
	th := c.u.th
	gs := &c.gset
	re := &gs.lists.role
	r := re.role
	col := colorText
	if re.color != 0 {
		col = rgb(uint32(re.color))
	}
	nameLabel := func(gtx layout.Context) layout.Dimensions {
		l := bold(label(th, 15, col, "Wumpus"))
		if re.second != 0 {
			return gradientName(gtx, l, col, rgb(uint32(re.second)))
		}
		return l.Layout(gtx)
	}
	swatch := func(gtx layout.Context, click *widget.Clickable, fill colorT, size image.Point, chosen bool) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if editable {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			rr := image.Rectangle{Max: size}
			paint.FillShape(gtx.Ops, fill, clip.UniformRRect(rr, gtx.Dp(4)).Op(gtx.Ops))
			if chosen {
				paint.FillShape(gtx.Ops, colorHeader, clip.Stroke{Path: clip.UniformRRect(rr, gtx.Dp(4)).Path(gtx.Ops), Width: float32(gtx.Dp(2))}.Op())
				gtx.Constraints = layout.Exact(size)
				layout.Center.Layout(gtx, bold(label(th, 16, colorHeader, "✓")).Layout)
			}
			return layout.Dimensions{Size: size}
		})
	}
	rows := []layout.Widget{
		section(th, "Role Name", ""),
		func(gtx layout.Context) layout.Dimensions {
			if !editable || r.Managed {
				return rounded(gtx, colorServers, 6, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(10)).Layout(gtx, label(th, 15, colorMuted, r.Name).Layout)
				})
			}
			return textField(gtx, th, &re.name, "Role name")
		},
		gap(16),
		section(th, "Role Color", "Members use the color of the highest role they have on the roles list."),
		func(gtx layout.Context) layout.Dimensions {
			big := image.Pt(gtx.Dp(66), gtx.Dp(50))
			small := image.Pt(gtx.Dp(22), gtx.Dp(22))
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return swatch(gtx, &re.defaultBtn, rgb(0x99aab5), big, re.color == 0)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					custom := !slices.Contains(rolePresets[:], uint32(re.color)) && re.color != 0
					fill := colorServers
					if custom {
						fill = col
					}
					dims := swatch(gtx, &re.custom, fill, big, custom)
					if !custom {
						gtx.Constraints = layout.Exact(big)
						layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuEdit, 18, colorMuted) })
					}
					return dims
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gapPx := gtx.Dp(6)
					for i, p := range rolePresets {
						x, y := (i%10)*(small.X+gapPx), (i/10)*(small.Y+gapPx)
						t := offset(gtx, x, y)
						swatch(gtx, &re.presets[i], rgb(p), small, uint32(re.color) == p)
						t.Pop()
					}
					return layout.Dimensions{Size: image.Pt(10*small.X+9*gapPx, 2*small.Y+gapPx)}
				}),
			)
		},
	}
	if re.pickerOpen && editable {
		rows = append(rows, layout.Spacer{Height: unit.Dp(10)}.Layout, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(320))
			return re.picker.layout(gtx)
		})
	}
	rows = append(rows,
		gap(16),
		section(th, "Role Gradient", "Fades the name from the role's color to this one."),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &re.gradBtn, "Gradient name", "", re.second != 0)
		},
		func(gtx layout.Context) layout.Dimensions {
			if re.second == 0 {
				return layout.Dimensions{}
			}
			small := image.Pt(gtx.Dp(22), gtx.Dp(22))
			gapPx := gtx.Dp(6)
			for i, p := range rolePresets {
				x, y := (i%10)*(small.X+gapPx), (i/10)*(small.Y+gapPx)
				t := offset(gtx, x, y)
				swatch(gtx, &re.secondPresets[i], rgb(p), small, uint32(re.second) == p)
				t.Pop()
			}
			return layout.Dimensions{Size: image.Pt(10*small.X+9*gapPx, 2*small.Y+gapPx)}
		},
	)
	iconText := "Upload an image under 256 KB. We recommend at least 64x64 pixels. Members see the icon of their highest role. Needs Server Boost level 2."
	rows = append(rows,
		gap(16),
		section(th, "Role Icon", iconText),
		func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if r.Icon == "" {
					return icon(gtx, iconMenuChannel, 32, colorMuted)
				}
				url := "https://cdn.discordapp.com/role-icons/" + r.ID.String() + "/" + string(r.Icon) + ".png?size=64"
				return c.u.picture(gtx, url, 32, colorServers, r.Name, 0)
			}), layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout)}
			if editable {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &re.iconBtn, colorAccent, "Choose Image")
				}))
				if r.Icon != "" {
					children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &re.iconRemove, colorSecondaryButton, "Remove")
					}))
				}
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		// How a message looks with the role, on the four themes.
		func(gtx layout.Context) layout.Dimensions {
			var lines []layout.FlexChild
			for _, bg := range []uint32{0x323339, 0x2b2d31, 0x0a0a0c, 0xffffff} {
				bg := bg
				text := colorText
				if bg == 0xffffff {
					text = rgb(0x313338)
				}
				lines = append(lines, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return rounded(gtx, rgb(bg), 0, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return avatar(gtx, th, 36, colorAccent, "Wumpus", -1) }),
								layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
								layout.Flexed(1, stacked(nameLabel, label(th, 14, text, "rocks are really old").Layout)),
							)
						})
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
		},
		gap(16),
		section(th, "Role Emoji", "A unicode emoji shown beside the members of the role. Setting one replaces the role icon. Needs Server Boost level 2."),
		func(gtx layout.Context) layout.Dimensions {
			if !editable || r.Managed {
				return label(th, 15, colorText, r.UnicodeEmoji).Layout(gtx)
			}
			return textField(gtx, th, &re.emoji, "Emoji")
		},
		gap(16),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &re.hoistBtn, "Display role members separately from online members", "", r.Hoist)
		},
		gap(16),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &re.mentionBtn, "Allow anyone to @mention this role",
				"Members with \"Mention @everyone, @here and All Roles\" can always mention this role.", r.Mentionable)
		},
	)
	changed := strings.TrimSpace(re.name.Text()) != re.origName && !r.Managed || re.color != re.origColor || re.second != re.origSecond || strings.TrimSpace(re.emoji.Text()) != re.origEmoji
	if changed && editable {
		rows = append(rows, saveBar(th, true, false, "", "", &re.reset, &re.save))
	}
	return rows
}

func (c *chatView) rolePermissionRows(editable bool) []layout.Widget {
	th := c.u.th
	gs := &c.gset
	re := &gs.lists.role
	q := strings.ToLower(strings.TrimSpace(re.permSearch.Text()))
	if re.permBtns == nil {
		re.permBtns = map[uint]*widget.Clickable{}
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return textField(gtx, th, &re.permSearch, "Search permissions")
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	first := true
	for _, g := range rolePermGroups() {
		var shown []channelPerm
		for _, p := range g.perms {
			if q == "" || strings.Contains(strings.ToLower(p.name+" "+p.about), q) {
				shown = append(shown, p)
			}
		}
		if len(shown) == 0 {
			continue
		}
		title, withClear := g.title, first
		first = false
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, bold(label(th, 18, colorHeader, title)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !withClear || !editable {
							return layout.Dimensions{}
						}
						return re.clearPerms.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							return label(th, 13, colorLink, "Clear permissions").Layout(gtx)
						})
					}),
				)
			})
		})
		for _, p := range shown {
			p := p
			click := re.permBtns[p.bit]
			if click == nil {
				click = new(widget.Clickable)
				re.permBtns[p.bit] = click
			}
			on := re.role.Permissions.Has(discord.Permissions(1) << p.bit)
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if !editable {
						// A look only.
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, stacked(bold(label(th, 15, colorMuted, p.name)).Layout, label(th, 13, colorMuted, p.about).Layout)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return switchToggle(gtx, on) }),
						)
					}
					return switchRow(gtx, th, click, p.name, p.about, on)
				})
			}, func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 0) })
		}
	}
	return rows
}

func (c *chatView) roleMemberRows(inRole []discord.Member, editable bool) []layout.Widget {
	th := c.u.th
	gs := &c.gset
	re := &gs.lists.role
	q := strings.ToLower(strings.TrimSpace(re.memberSearch.Text()))
	canAdd := editable && !re.role.Managed
	rows := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return textField(gtx, th, &re.memberSearch, "Search members")
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				bg := colorAccent
				if !canAdd {
					bg = withAlpha(colorAccent, 0x60)
				}
				return button(gtx, th, &re.addMember, bg, "Add Members")
			}),
		)
	}, layout.Spacer{Height: unit.Dp(12)}.Layout}
	if re.removeMember == nil {
		re.removeMember = map[discord.UserID]*widget.Clickable{}
	}
	for _, m := range inRole {
		m := m
		name := c.memberName(gs.guildID, m.User.ID, &m)
		if q != "" && !strings.Contains(strings.ToLower(name+" "+m.User.Username), q) {
			continue
		}
		click := clickFor(&re.removeMember, m.User.ID)
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, c.authorAvatarURL(gs.guildID, m.User), 28, colorFor(uint64(m.User.ID)), name, -1)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(bold(label(th, 14, colorHeader, name)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !m.User.Bot {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, botTag(th))
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						dims := layout.Inset{Left: unit.Dp(6)}.Layout(gtx, oneLine(label(th, 13, colorMuted, m.User.Username)).Layout)
						dims.Size.X = gtx.Constraints.Max.X
						return dims
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !canAdd {
							return layout.Dimensions{}
						}
						return iconButton(gtx, click, iconClose, colorMuted, colorRed)
					}),
				)
			})
		})
	}
	if len(inRole) == 0 {
		rows = append(rows, settingsText(th, "No members have this role."))
	}
	return rows
}
