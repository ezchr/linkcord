package gui

import (
	"image"
	"slices"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconMenuBoost    = mustIcon(icons.ActionStars)
	iconMenuInvite   = mustIcon(icons.SocialPersonAdd)
	iconMenuSettings = mustIcon(icons.ActionSettings)
	iconMenuChannel  = mustIcon(icons.ContentAddCircle)
	iconMenuCategory = mustIcon(icons.FileCreateNewFolder)
	iconMenuThreads  = mustIcon(icons.CommunicationForum)
	iconMenuNotify   = mustIcon(icons.SocialNotifications)
	iconMenuPrivacy  = mustIcon(icons.HardwareSecurity)
	iconMenuEdit     = mustIcon(icons.EditorModeEdit)
	iconMenuHide     = mustIcon(icons.ActionVisibilityOff)
	iconMenuLeave    = mustIcon(icons.ActionExitToApp)
	iconMenuDown     = mustIcon(icons.NavigationExpandMore)
)

// The pages of the server's menu.
const (
	guildMenuMain = iota
	guildMenuNotify
	guildMenuPrivacy
	guildMenuThreads
)

// guildMenu is the menu that opens from the server's name, over its
// channels, as Discord's.
type guildMenu struct {
	btn     widget.Clickable
	guildID discord.GuildID
	page    int
	// open reports whether the menu open is this one.
	open bool
	// tags are the tags of the servers, found among their members, nil
	// where none was.
	tags map[discord.GuildID]*discord.PrimaryGuild
	// settings are the notification settings changed here, shown before
	// Discord says so.
	settings map[discord.GuildID]gateway.UserGuildSetting
}

// layoutGuildHeader is the server's name at the top of its channels, which
// opens its menu.
func (c *chatView) layoutGuildHeader(gtx layout.Context, title string) layout.Dimensions {
	th := c.u.th
	gm := &c.gmenu
	if gm.open && !c.menu.open {
		gm.open = false
	}
	if gm.btn.Clicked(gtx) {
		if gm.open {
			c.closeMenu()
			gm.open = false
		} else {
			c.openGuildMenu(c.selGuild, image.Pt(gtx.Dp(72)+gtx.Dp(8), gtx.Dp(52)))
		}
	}
	height := gtx.Dp(48)
	size := image.Pt(gtx.Constraints.Max.X, height)
	gtx.Constraints = layout.Exact(size)
	return gm.btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if gm.btn.Hovered() || gm.open {
			paint.FillShape(gtx.Ops, colorHover, clip.Rect{Max: size}.Op())
		}
		paint.FillShape(gtx.Ops, colorServers, clip.Rect{Min: image.Pt(0, height-1), Max: size}.Op())
		layout.Inset{Left: unit.Dp(16), Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						// The members online and in all, small, under the name.
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(oneLine(bold(label(th, 16, colorHeader, title))).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return c.layoutGuildCounts(gtx, c.selGuild)
							}))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						ic := iconMenuDown
						if gm.open {
							ic = iconClose
						}
						return icon(gtx, ic, 18, colorHeader)
					}),
				)
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// openGuildMenu opens the menu of the server guildID at pos.
func (c *chatView) openGuildMenu(guildID discord.GuildID, pos image.Point) {
	gm := &c.gmenu
	gm.guildID, gm.page = guildID, guildMenuMain
	c.findGuildTag(guildID)
	c.openMenu(pos, 250, c.guildMenuItems)
	gm.open = true
}

// guildPerms is what the user may do in the server as a whole, from their
// roles, without the overwrites of any channel.
func (c *chatView) guildPerms(guildID discord.GuildID) discord.Permissions {
	if c.demo {
		return allPermissions
	}
	g, err := c.st.Cabinet.Guild(guildID)
	if err != nil {
		return 0
	}
	if g.OwnerID == c.me.ID {
		return allPermissions
	}
	var p discord.Permissions
	if r, err := c.st.Cabinet.Role(guildID, discord.RoleID(guildID)); err == nil {
		p |= r.Permissions
	}
	if m, err := c.st.Cabinet.Member(guildID, c.me.ID); err == nil {
		for _, id := range m.RoleIDs {
			if r, err := c.st.Cabinet.Role(guildID, id); err == nil {
				p |= r.Permissions
			}
		}
	}
	if p.Has(discord.PermissionAdministrator) {
		return allPermissions
	}
	return p
}

// findGuildTag looks for the server's tag on the user and its members, once.
func (c *chatView) findGuildTag(guildID discord.GuildID) {
	gm := &c.gmenu
	if gm.tags == nil {
		gm.tags = map[discord.GuildID]*discord.PrimaryGuild{}
	}
	if _, ok := gm.tags[guildID]; ok {
		return
	}
	var tag *discord.PrimaryGuild
	if pg := c.me.PrimaryGuild; pg != nil && pg.GuildID == guildID && pg.Tag != "" {
		tag = pg
	} else if members, err := c.st.Cabinet.Members(guildID); err == nil {
		for _, m := range members {
			if pg := m.User.PrimaryGuild; pg != nil && pg.GuildID == guildID && pg.Tag != "" {
				tag = pg
				break
			}
		}
	}
	gm.tags[guildID] = tag
}

// guildSetting is the user's notification settings for the server.
func (c *chatView) guildSetting(guildID discord.GuildID) gateway.UserGuildSetting {
	if s, ok := c.gmenu.settings[guildID]; ok {
		return s
	}
	if c.st.MutedState == nil {
		return gateway.UserGuildSetting{GuildID: guildID}
	}
	return c.st.MutedState.GuildSettings(guildID)
}

// patchGuildSetting changes the user's notification settings for the
// server: change applies it to what is shown, body to what Discord keeps.
func (c *chatView) patchGuildSetting(guildID discord.GuildID, change func(*gateway.UserGuildSetting), body map[string]any) {
	gm := &c.gmenu
	if gm.settings == nil {
		gm.settings = map[discord.GuildID]gateway.UserGuildSetting{}
	}
	s := c.guildSetting(guildID)
	change(&s)
	gm.settings[guildID] = s
	c.menu.stay = true
	if c.demo {
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointMe+"/guilds/"+guildID.String()+"/settings", httputil.WithJSONBody(body))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not change the notification settings", err) })
		}
	}()
}

func menuIcon(ic *widget.Icon, col colorT) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 18, col) }
}

// guildMenuItems are the server menu's lines, on the page shown.
func (c *chatView) guildMenuItems() []menuItem {
	gm := &c.gmenu
	guildID := gm.guildID
	g, err := c.st.Cabinet.Guild(guildID)
	if err != nil {
		return []menuItem{{title: true, text: "This server is not loaded"}}
	}
	page := func(p int) func() {
		return func() {
			gm.page = p
			c.menu.stay = true
			c.menu.list.Position = layout.Position{}
		}
	}
	back := menuItem{key: "back", text: "Back", back: true, act: page(guildMenuMain)}
	switch gm.page {
	case guildMenuNotify:
		return append([]menuItem{back}, c.guildNotifyItems(guildID)...)
	case guildMenuPrivacy:
		return append([]menuItem{back}, c.guildPrivacyItems(guildID)...)
	case guildMenuThreads:
		return append([]menuItem{back}, c.guildThreadItems(guildID)...)
	}

	perms := c.guildPerms(guildID)
	items := []menuItem{
		{text: "Server Boost", lead: menuIcon(iconMenuBoost, colorBoost), act: func() {
			c.selectGuildIfNot(guildID)
			c.openGuildInfo(boostsRowID)
		}},
	}
	if tag := gm.tags[guildID]; tag != nil {
		wearing := c.me.PrimaryGuild != nil && c.me.PrimaryGuild.GuildID == guildID && c.me.PrimaryGuild.Enabled
		items = append(items, menuItem{key: "tag", text: "Server Tag", check: checked(wearing), lead: func(gtx layout.Context) layout.Dimensions {
			return c.guildTag(gtx, guildID, tag.Badge, tag.Tag)
		}, act: func() { c.wearGuildTag(guildID, *tag, !wearing) }})
	}
	items = append(items, menuItem{sep: true})
	if ch, ok := c.inviteChannel(guildID); ok {
		items = append(items, menuItem{text: "Invite to Server", col: colorAccent, lead: menuIcon(iconMenuInvite, colorAccent), act: func() { c.copyInvite(ch) }})
	}
	if perms.Has(discord.PermissionManageGuild) || perms.Has(discord.PermissionManageRoles) || perms.Has(discord.PermissionBanMembers) {
		items = append(items, menuItem{text: "Server Settings", lead: menuIcon(iconMenuSettings, colorText), act: func() { c.openGuildSettings(guildID) }})
	}
	if perms.Has(discord.PermissionManageChannels) {
		items = append(items,
			menuItem{text: "Create Channel", lead: menuIcon(iconMenuChannel, colorText), act: func() { c.openCreateChannel(guildID, false) }},
			menuItem{text: "Create Category", lead: menuIcon(iconMenuCategory, colorText), act: func() { c.openCreateChannel(guildID, true) }},
		)
	}
	items = append(items,
		menuItem{text: "Active Threads", sub: true, lead: menuIcon(iconMenuThreads, colorText), act: page(guildMenuThreads)},
		menuItem{sep: true},
		menuItem{text: "Notification Settings", sub: true, lead: menuIcon(iconMenuNotify, colorText), act: page(guildMenuNotify)},
		menuItem{text: "Privacy Settings", sub: true, lead: menuIcon(iconMenuPrivacy, colorText), act: func() {
			if !c.settings.acct.loaded {
				c.loadAccount()
			}
			page(guildMenuPrivacy)()
		}},
		menuItem{sep: true},
		menuItem{text: "Edit Server Profile", lead: menuIcon(iconMenuEdit, colorText), act: func() { c.openServerProfile(guildID) }},
		menuItem{text: "Hide Muted Channels", check: checked(c.u.app.hidesMuted(guildID)), lead: menuIcon(iconMenuHide, colorText), act: func() {
			c.u.app.setHideMuted(guildID, !c.u.app.hidesMuted(guildID))
			c.u.saveAppSettings()
			c.rebuildRows()
			c.menu.stay = true
		}},
	)
	if g.OwnerID != c.me.ID {
		items = append(items, menuItem{sep: true}, menuItem{text: "Leave Server", col: colorRed, lead: menuIcon(iconMenuLeave, colorRed), act: func() { c.askLeaveGuild(guildID, g.Name) }})
	}
	items = append(items, menuItem{sep: true}, menuItem{text: "Copy Server ID", lead: menuIcon(iconCopy, colorText), act: func() { c.copyText = guildID.String() }})
	return items
}

// selectGuildIfNot shows the server, if another is shown.
func (c *chatView) selectGuildIfNot(guildID discord.GuildID) {
	if c.selGuild != guildID {
		c.selectGuild(guildID)
	}
}

// guildNotifyItems are the server's notification settings, as Discord's.
func (c *chatView) guildNotifyItems(guildID discord.GuildID) []menuItem {
	s := c.guildSetting(guildID)
	level := func(text string, n gateway.UserNotification) menuItem {
		return menuItem{key: "notify" + text, text: text, check: checked(s.Notifications == n), act: func() {
			c.patchGuildSetting(guildID, func(s *gateway.UserGuildSetting) { s.Notifications = n }, map[string]any{"message_notifications": n})
		}}
	}
	muteBody := map[string]any{"muted": !s.Muted}
	if !s.Muted {
		muteBody["mute_config"] = map[string]any{"selected_time_window": -1, "end_time": nil}
	}
	return []menuItem{
		{title: true, text: "Notification Settings"},
		{key: "mute", text: "Mute Server", check: checked(s.Muted), act: func() {
			c.patchGuildSetting(guildID, func(s *gateway.UserGuildSetting) { s.Muted = !s.Muted; s.MuteConfig = nil }, muteBody)
		}},
		{sep: true},
		level("All Messages", gateway.AllNotifications),
		level("Only @mentions", gateway.OnlyMentions),
		level("Nothing", gateway.NoNotifications),
		{sep: true},
		{key: "everyone", text: "Suppress @everyone and @here", check: checked(s.SuppressEveryone), act: func() {
			c.patchGuildSetting(guildID, func(s *gateway.UserGuildSetting) { s.SuppressEveryone = !s.SuppressEveryone }, map[string]any{"suppress_everyone": !s.SuppressEveryone})
		}},
		{key: "roles", text: "Suppress All Role @mentions", check: checked(s.SuppressRoles), act: func() {
			c.patchGuildSetting(guildID, func(s *gateway.UserGuildSetting) { s.SuppressRoles = !s.SuppressRoles }, map[string]any{"suppress_roles": !s.SuppressRoles})
		}},
	}
}

// guildPrivacyItems are whether members of the server may message the user.
func (c *chatView) guildPrivacyItems(guildID discord.GuildID) []menuItem {
	a := &c.settings.acct
	if !a.loaded {
		return []menuItem{{title: true, text: "Loading…"}}
	}
	allowed := !slices.Contains(a.settings.RestrictedGuilds, guildID)
	return []menuItem{
		{title: true, text: "Privacy Settings"},
		{key: "dms", text: "Direct Messages from Members", check: checked(allowed), act: func() {
			list := slices.DeleteFunc(slices.Clone(a.settings.RestrictedGuilds), func(id discord.GuildID) bool { return id == guildID })
			if allowed {
				list = append(list, guildID)
			}
			if list == nil {
				list = []discord.GuildID{}
			}
			a.settings.RestrictedGuilds = list
			c.patchSettings(map[string]any{"restricted_guilds": list})
			c.menu.stay = true
		}},
	}
}

// guildThreadItems are the server's open threads the user can see.
func (c *chatView) guildThreadItems(guildID discord.GuildID) []menuItem {
	items := []menuItem{{title: true, text: "Active Threads"}}
	channels, _ := c.st.Cabinet.Channels(guildID)
	var threads []discord.Channel
	for _, ch := range channels {
		switch ch.Type {
		case discord.GuildPublicThread, discord.GuildPrivateThread, discord.GuildAnnouncementThread:
		default:
			continue
		}
		if ch.ThreadMetadata != nil && ch.ThreadMetadata.Archived || !c.can(ch.ParentID, discord.PermissionViewChannel) {
			continue
		}
		threads = append(threads, ch)
	}
	slices.SortFunc(threads, func(a, b discord.Channel) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	if len(threads) == 0 {
		return append(items, menuItem{key: "none", text: "No active threads", col: colorMuted})
	}
	for _, th := range threads[:min(len(threads), 50)] {
		th := th
		items = append(items, menuItem{key: "thread" + th.ID.String(), text: th.Name, lead: menuIcon(iconMenuThreads, colorMuted), act: func() {
			c.selectGuildIfNot(guildID)
			c.selectChannel(th)
		}})
	}
	return items
}

// wearGuildTag shows the server's tag beside the user's name, or stops.
func (c *chatView) wearGuildTag(guildID discord.GuildID, tag discord.PrimaryGuild, wear bool) {
	apply := func() {
		if wear {
			tag.Enabled = true
			tag.GuildID = guildID
			c.me.PrimaryGuild = &tag
		} else {
			c.me.PrimaryGuild = nil
		}
	}
	if c.demo {
		apply()
		return
	}
	body := map[string]any{"identity_guild_id": guildID.String(), "identity_enabled": true}
	if !wear {
		body = map[string]any{"identity_guild_id": nil, "identity_enabled": false}
	}
	go func() {
		err := c.st.Client.FastRequest("PUT", api.EndpointMe+"/clan", httputil.WithJSONBody(body))
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not change the server tag", err)
				return
			}
			apply()
		})
	}()
}

// askLeaveGuild asks whether to leave the server, and leaves it.
func (c *chatView) askLeaveGuild(guildID discord.GuildID, name string) {
	text := "Are you sure you want to leave " + name + "? You won't be able to rejoin this server unless you are re-invited."
	c.prompt("Leave '"+name+"'", "Leave Server", true, []string{text}, nil, func([]string) {
		if c.demo {
			c.u.voiceInfo("The demo does not leave servers")
			return
		}
		go func() {
			err := c.st.Client.FastRequest("DELETE", api.EndpointMe+"/guilds/"+guildID.String(), httputil.WithJSONBody(map[string]any{"lurking": false}))
			c.u.post(func() {
				if err != nil {
					c.u.voiceFailed("Could not leave the server", err)
					return
				}
				if c.selGuild == guildID {
					c.selectGuild(discord.NullGuildID)
				}
			})
		}()
	})
}

// hidesMuted reports whether the server's muted channels are hidden.
func (a appSettings) hidesMuted(guildID discord.GuildID) bool {
	return slices.Contains(a.HideMuted, guildID)
}

func (a *appSettings) setHideMuted(guildID discord.GuildID, hide bool) {
	a.HideMuted = slices.DeleteFunc(a.HideMuted, func(id discord.GuildID) bool { return id == guildID })
	if hide {
		a.HideMuted = append(a.HideMuted, guildID)
	}
}
