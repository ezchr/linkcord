package gui

import (
	"cmp"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// userMenuPage is the list the menu of a user shows: its own, or that of
// one of its items.
type userMenuPage int

const (
	userMenuMain userMenuPage = iota
	userMenuRoles
	userMenuMove
	userMenuTimeout
	userMenuInvite
)

// userMenu is the menu of what can be done to a user, opened with a right
// click on them in a voice channel.
type userMenu struct {
	user    discord.User
	guildID discord.GuildID
	// channel is the voice channel the user is in.
	channel discord.ChannelID
	page    userMenuPage
	volume  slider
	// soundboard is the slider of how loud their soundboard sounds play.
	soundboard slider
	// streamVolume is the slider of their stream's sound, when watched.
	streamVolume slider
}

// timeouts are the lengths of time a member can be timed out for, as Discord
// offers them.
var timeouts = []struct {
	name string
	d    time.Duration
}{
	{"60 seconds", time.Minute},
	{"5 minutes", 5 * time.Minute},
	{"10 minutes", 10 * time.Minute},
	{"1 hour", time.Hour},
	{"1 day", 24 * time.Hour},
	{"1 week", 7 * 24 * time.Hour},
}

// openUserMenu opens, at the pointer, the menu of the user in the voice
// channel chID of guildID.
func (c *chatView) openUserMenu(userID discord.UserID, guildID discord.GuildID, chID discord.ChannelID) {
	user := discord.User{ID: userID, Username: "User"}
	if m, err := c.st.Cabinet.Member(guildID, userID); err == nil && m.User.ID.IsValid() {
		user = m.User
	} else if u, err := c.st.Cabinet.User(userID); err == nil {
		user = *u
	} else if userID == c.me.ID {
		user = c.me
	}
	c.umenu = userMenu{user: user, guildID: guildID, channel: chID}
	c.openMenu(c.pointerAt, 220, c.userMenuItems)
}

// myPerms is what the user may do in the channel chID.
func (c *chatView) myPerms(chID discord.ChannelID) discord.Permissions {
	if c.demo {
		return allPermissions
	}
	// From the cache: the menu is built while drawing, which must not wait
	// for Discord. Until it has loaded, no moderation shows.
	p, ok := c.cachedPerms(chID)
	if !ok {
		return 0
	}
	// Arikawa's PermissionAll, which owners and administrators get, lacks
	// the newer permissions, such as timing members out.
	if p.Has(discord.PermissionAdministrator) || p == discord.PermissionAll {
		return allPermissions
	}
	return p
}

// allPermissions is every permission, those Arikawa knows and newer ones.
const allPermissions = ^discord.Permissions(0)

// topRole is the position of the highest role of the member, 0 for none.
func (c *chatView) topRole(guildID discord.GuildID, userID discord.UserID) int {
	m, err := c.st.Cabinet.Member(guildID, userID)
	if err != nil {
		return 0
	}
	top := 0
	for _, id := range m.RoleIDs {
		if r, err := c.st.Cabinet.Role(guildID, id); err == nil {
			top = max(top, r.Position)
		}
	}
	return top
}

// outranks reports whether the user is above target in guildID, which
// Discord requires to kick, ban, time out, rename or change the roles of a
// member.
func (c *chatView) outranks(guildID discord.GuildID, target discord.UserID) bool {
	g, err := c.st.Cabinet.Guild(guildID)
	if err != nil || target == g.OwnerID || target == c.me.ID {
		return false
	}
	if c.me.ID == g.OwnerID {
		return true
	}
	return c.topRole(guildID, c.me.ID) > c.topRole(guildID, target)
}

// canGiveRole reports whether the user may give or take away role.
func (c *chatView) canGiveRole(guildID discord.GuildID, role discord.Role) bool {
	if role.Managed || discord.GuildID(role.ID) == guildID {
		return false
	}
	if g, err := c.st.Cabinet.Guild(guildID); err == nil && g.OwnerID == c.me.ID {
		return true
	}
	return role.Position < c.topRole(guildID, c.me.ID)
}

// userMenuItems lists what the user may do to the user of the menu: the
// moderation only with the permissions for it.
func (c *chatView) userMenuItems() []menuItem {
	um := &c.umenu
	switch um.page {
	case userMenuRoles:
		return c.roleItems()
	case userMenuMove:
		return c.moveItems()
	case userMenuTimeout:
		return c.timeoutItems()
	case userMenuInvite:
		return c.inviteItems()
	}
	u := c.u
	id := um.user.ID
	self := id == c.me.ID
	guildID := um.guildID
	chID := um.channel
	if !chID.IsValid() && c.selChannel != nil {
		chID = c.selChannel.ID
	}
	perms := c.myPerms(chID)
	has := func(p discord.Permissions) bool { return perms.Has(p) }
	name := c.memberName(guildID, id, nil)
	var vs *discord.VoiceState
	if um.channel.IsValid() {
		vs, _ = c.st.Cabinet.VoiceState(guildID, id)
	}
	sub := func(page userMenuPage) func() {
		return func() {
			um.page = page
			c.menu.stay = true
			c.menu.list.Position = layout.Position{}
		}
	}
	stay := func(f func()) func() {
		return func() {
			f()
			c.menu.stay = true
		}
	}

	items := []menuItem{{text: "Profile", act: func() { c.openProfile(um.user, guildID) }}}
	if !self {
		noteText := "Add Note"
		if c.note(id) != "" {
			noteText = "Edit Note"
		}
		items = append(items,
			menuItem{text: "Message", act: func() { c.openDM(id) }},
			menuItem{text: "Call", act: func() { c.startCall(id) }},
			menuItem{text: noteText, act: func() { c.promptNote(um.user) }},
		)
		if um.channel.IsValid() {
			items = append(items,
				menuItem{sep: true},
				menuItem{w: c.layoutVolumeItem},
				menuItem{text: "Mute", check: checked(u.voiceSettings().Muted[id]), act: stay(func() { u.toggleLocalMute(id) })},
				menuItem{text: "Mute Soundboard", check: checked(slices.Contains(u.app.SoundboardMuted, id)), act: stay(func() {
					a := &u.app
					muted := slices.Contains(a.SoundboardMuted, id)
					a.SoundboardMuted = slices.DeleteFunc(a.SoundboardMuted, func(x discord.UserID) bool { return x == id })
					if !muted {
						a.SoundboardMuted = append(a.SoundboardMuted, id)
					}
					u.saveAppSettings()
				})},
				menuItem{w: c.layoutSoundboardVolumeItem},
			)
			// The sound of their stream, when it is watched.
			if s := c.watched(id); s != nil && s.w != nil {
				w := s.w
				items = append(items,
					menuItem{sep: true},
					menuItem{w: c.layoutStreamVolumeItem},
					menuItem{text: "Mute Stream", check: checked(w.AudioMuted()), act: stay(func() {
						muted := !w.AudioMuted()
						w.SetAudioMuted(muted)
						if c.streamMuted == nil {
							c.streamMuted = make(map[discord.UserID]bool)
						}
						c.streamMuted[id] = muted
					})},
				)
			}
		}
	} else if um.channel.IsValid() && um.channel == u.voiceChannel() {
		items = append(items,
			menuItem{sep: true},
			menuItem{text: "Mute", check: checked(u.voice.muted), act: stay(func() { u.toggleVoice(false); u.syncDemoVoice() })},
			menuItem{text: "Deafen", check: checked(u.voice.deafened), act: stay(func() { u.toggleVoice(true); u.syncDemoVoice() })},
		)
	}

	var social []menuItem
	if guildID.IsValid() && (self && has(discord.PermissionChangeNickname) || !self && has(discord.PermissionManageNicknames) && c.outranks(guildID, id)) {
		social = append(social, menuItem{text: "Change Nickname", act: func() { c.promptNickname(um.user, guildID) }})
	}
	if !self {
		social = append(social, menuItem{text: "Invite to Server", sub: true, act: sub(userMenuInvite)})
		switch c.relationship(id) {
		case discord.FriendRelationship:
			social = append(social, menuItem{text: "Remove Friend", col: colorRed, act: func() { c.setFriend(um.user, false) }})
		case discord.IncomingFriendRequest:
			social = append(social, menuItem{text: "Accept Friend Request", act: func() { c.setFriend(um.user, true) }})
		case discord.SentFriendRequest:
			social = append(social, menuItem{text: "Cancel Friend Request", act: func() { c.setFriend(um.user, false) }})
		case discord.BlockedRelationship:
		default:
			social = append(social, menuItem{text: "Add Friend", act: func() { c.setFriend(um.user, true) }})
		}
		if c.relationship(id) == discord.BlockedRelationship {
			social = append(social, menuItem{text: "Unblock", act: func() { c.setBlocked(um.user, false) }})
		} else {
			social = append(social, menuItem{text: "Block", col: colorRed, act: func() { c.setBlocked(um.user, true) }})
		}
	}
	if len(social) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, social...)
	}

	var server []menuItem
	if guildID.IsValid() && has(discord.PermissionManageRoles) && len(c.givableRoles(guildID)) > 0 && (self || c.outranks(guildID, id)) {
		server = append(server, menuItem{text: "Roles", sub: true, act: sub(userMenuRoles)})
	}
	if um.channel.IsValid() && guildID.IsValid() && has(discord.PermissionMoveMembers) {
		server = append(server, menuItem{text: "Move To", sub: true, act: sub(userMenuMove)})
	}
	if len(server) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, server...)
	}

	var voice []menuItem
	if !self && guildID.IsValid() {
		// Joins the voice channels they go to, until stopped.
		following := c.following == id
		voice = append(voice, menuItem{text: "Follow in Voice", check: checked(following), act: func() {
			c.toggleFollow(guildID, id)
		}})
	}
	if vs != nil && guildID.IsValid() {
		if has(discord.PermissionMuteMembers) {
			on := vs.Mute
			voice = append(voice, menuItem{text: "Server Mute", col: colorRed, check: checked(on), act: stay(func() {
				c.patchMember("Could not server mute "+name, guildID, id, map[string]any{"mute": !on})
			})})
		}
		if has(discord.PermissionDeafenMembers) {
			on := vs.Deaf
			voice = append(voice, menuItem{text: "Server Deafen", col: colorRed, check: checked(on), act: stay(func() {
				c.patchMember("Could not server deafen "+name, guildID, id, map[string]any{"deaf": !on})
			})})
		}
		if self || has(discord.PermissionMoveMembers) {
			voice = append(voice, menuItem{text: "Disconnect", col: colorRed, act: func() {
				if self {
					u.leaveVoice()
					return
				}
				c.patchMember("Could not disconnect "+name, guildID, id, map[string]any{"channel_id": nil})
			}})
		}
	}
	if len(voice) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, voice...)
	}

	var mod []menuItem
	if guildID.IsValid() && c.outranks(guildID, id) {
		if has(discord.PermissionModerateMembers) {
			if c.timedOut(guildID, id) {
				mod = append(mod, menuItem{text: "Remove Timeout", col: colorRed, act: func() {
					c.patchMember("Could not remove the timeout of "+name, guildID, id, map[string]any{"communication_disabled_until": nil})
				}})
			} else {
				mod = append(mod, menuItem{text: "Timeout " + name, col: colorRed, sub: true, act: sub(userMenuTimeout)})
			}
		}
		if has(discord.PermissionKickMembers) {
			mod = append(mod, menuItem{text: "Kick " + name, col: colorRed, act: func() { c.promptKick(um.user, guildID) }})
		}
		if has(discord.PermissionBanMembers) {
			mod = append(mod, menuItem{text: "Ban " + name, col: colorRed, act: func() { c.promptBan(um.user, guildID) }})
		}
	}
	if len(mod) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, mod...)
	}

	items = append(items,
		menuItem{sep: true},
		menuItem{text: "Copy User ID", act: func() { c.copyText = id.String() }},
	)
	return items
}

// backItem goes back to the menu of the user from one of its lists.
func (c *chatView) backItem(text string) menuItem {
	return menuItem{key: "back", text: text, back: true, act: func() {
		c.umenu.page = userMenuMain
		c.menu.stay = true
		c.menu.list.Position = layout.Position{}
	}}
}

// layoutVolumeItem is the slider of how loud the user of the menu is played,
// from silent to twice as loud.
func (c *chatView) layoutVolumeItem(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	um := &c.umenu
	id := um.user.ID
	if r := um.volume.update(gtx); r.changed || r.released {
		c.u.setUserVolume(id, r.value*2)
		if r.released {
			c.u.saveVoiceSettings()
		}
	}
	vol := c.u.userVolume(id)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 14, colorText, "User Volume").Layout),
				layout.Rigid(label(th, 12, colorMuted, fmt.Sprintf("%d%%", int(vol*100+0.5))).Layout),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return um.volume.layout(gtx, 0, vol/2)
		}),
	)
}

// layoutStreamVolumeItem is the slider of how loud the stream watched of
// the user of the menu plays, from silent to twice as loud.
func (c *chatView) layoutStreamVolumeItem(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	um := &c.umenu
	s := c.watched(um.user.ID)
	if s == nil || s.w == nil {
		return layout.Dimensions{}
	}
	w := s.w
	if r := um.streamVolume.update(gtx); r.changed || r.released {
		vol := snapVolume(r.value * 2)
		w.SetAudioVolume(vol)
		if c.streamVolumes == nil {
			c.streamVolumes = make(map[discord.UserID]float32)
		}
		c.streamVolumes[um.user.ID] = vol
	}
	vol := w.AudioVolume()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 14, colorText, "Stream Volume").Layout),
				layout.Rigid(label(th, 12, colorMuted, fmt.Sprintf("%d%%", int(vol*100+0.5))).Layout),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return um.streamVolume.layout(gtx, 0, vol/2)
		}),
	)
}

// givableRoles are the roles of guildID the user may give or take away,
// highest first.
func (c *chatView) givableRoles(guildID discord.GuildID) []discord.Role {
	roles, _ := c.st.Cabinet.Roles(guildID)
	var out []discord.Role
	for _, r := range roles {
		if c.canGiveRole(guildID, r) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b discord.Role) int { return cmp.Compare(b.Position, a.Position) })
	return out
}

func (c *chatView) roleItems() []menuItem {
	um := &c.umenu
	items := []menuItem{c.backItem("Roles")}
	m, _ := c.st.Cabinet.Member(um.guildID, um.user.ID)
	for _, r := range c.givableRoles(um.guildID) {
		on := m != nil && slices.Contains(m.RoleIDs, r.ID)
		col := colorMuted
		if r.Color != 0 {
			col = rgb(uint32(r.Color))
		}
		items = append(items, menuItem{
			key: "role" + r.ID.String(), text: r.Name, check: checked(on),
			lead: func(gtx layout.Context) layout.Dimensions { return dot(gtx, col, 12) },
			act: func() {
				c.menu.stay = true
				c.setRole(um.guildID, um.user.ID, r, !on)
			},
		})
	}
	return items
}

func (c *chatView) moveItems() []menuItem {
	um := &c.umenu
	items := []menuItem{c.backItem("Move To")}
	chs, _ := c.st.Cabinet.Channels(um.guildID)
	chs = slices.DeleteFunc(slices.Clone(chs), func(ch discord.Channel) bool {
		return !isVoice(ch.Type) || ch.ID == um.channel
	})
	slices.SortFunc(chs, func(a, b discord.Channel) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})
	name := c.memberName(um.guildID, um.user.ID, nil)
	for _, ch := range chs {
		items = append(items, menuItem{
			key: "move" + ch.ID.String(), text: ch.Name,
			lead: func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconVoice, 16, colorChannel) },
			act: func() {
				if um.user.ID == c.me.ID {
					c.joinVoice(ch)
					return
				}
				c.patchMember("Could not move "+name, um.guildID, um.user.ID, map[string]any{"channel_id": ch.ID})
			},
		})
	}
	return items
}

func (c *chatView) timeoutItems() []menuItem {
	um := &c.umenu
	items := []menuItem{c.backItem("Timeout")}
	name := c.memberName(um.guildID, um.user.ID, nil)
	for _, t := range timeouts {
		items = append(items, menuItem{text: t.name, act: func() {
			until := time.Now().Add(t.d).UTC().Format(time.RFC3339)
			c.patchMember("Could not time out "+name, um.guildID, um.user.ID, map[string]any{"communication_disabled_until": until})
		}})
	}
	return items
}

// inviteItems are the servers the user can invite the user of the menu to.
func (c *chatView) inviteItems() []menuItem {
	um := &c.umenu
	items := []menuItem{c.backItem("Invite to Server")}
	for _, gid := range c.guildOrder {
		g, err := c.st.Cabinet.Guild(gid)
		if err != nil {
			continue
		}
		if _, err := c.st.Cabinet.Member(gid, um.user.ID); err == nil {
			continue
		}
		ch, ok := c.inviteChannel(gid)
		if !ok {
			continue
		}
		icon := cdnSize(g.IconURLWithType(discord.PNGImage))
		items = append(items, menuItem{
			key: "invite" + gid.String(), text: g.Name,
			lead: func(gtx layout.Context) layout.Dimensions {
				return c.u.picture(gtx, icon, 20, colorSidebar, g.Name, -1)
			},
			act: func() { c.sendInvite(ch, um.user) },
		})
	}
	if len(items) == 1 {
		items = append(items, menuItem{title: true, text: "No server to invite them to"})
	}
	return items
}

// inviteChannel is the channel of guildID to invite people to: the one new
// members are welcomed in, or else the first the user may invite to.
func (c *chatView) inviteChannel(guildID discord.GuildID) (discord.ChannelID, bool) {
	can := func(id discord.ChannelID) bool { return c.myPerms(id).Has(discord.PermissionCreateInstantInvite) }
	if g, err := c.st.Cabinet.Guild(guildID); err == nil && g.SystemChannelID.IsValid() && can(g.SystemChannelID) {
		return g.SystemChannelID, true
	}
	chs, _ := c.st.Cabinet.Channels(guildID)
	slices.SortFunc(chs, func(a, b discord.Channel) int { return cmp.Compare(a.Position, b.Position) })
	for _, ch := range chs {
		if ch.Type == discord.GuildText && can(ch.ID) {
			return ch.ID, true
		}
	}
	return 0, false
}

// relationship is what the user is to the user with id: friend, blocked…
func (c *chatView) relationship(id discord.UserID) discord.RelationshipType {
	for _, rel := range c.relationships() {
		if rel.UserID == id {
			return rel.Type
		}
	}
	return 0
}

// note is what the user wrote about the user with id.
func (c *chatView) note(id discord.UserID) string {
	if c.demo || c.st.NoteState == nil {
		return c.demoNotes[id]
	}
	return c.st.NoteState.Note(id)
}

// timedOut reports whether the member cannot talk for now.
func (c *chatView) timedOut(guildID discord.GuildID, id discord.UserID) bool {
	m, err := c.st.Cabinet.Member(guildID, id)
	return err == nil && m.CommunicationDisabledUntil.IsValid() && m.CommunicationDisabledUntil.Time().After(time.Now())
}

// patchMember changes the member with what body says, such as whether they
// are muted or what voice channel they are in.
func (c *chatView) patchMember(what string, guildID discord.GuildID, id discord.UserID, body map[string]any) {
	if c.demo {
		c.demoPatchMember(guildID, id, body)
		return
	}
	path := api.EndpointGuilds + guildID.String() + "/members/"
	if id == c.me.ID {
		path += "@me"
	} else {
		path += id.String()
	}
	go func() {
		if err := c.st.Client.FastRequest("PATCH", path, httputil.WithJSONBody(body)); err != nil {
			c.u.post(func() { c.u.voiceFailed(what, err) })
		}
	}()
}

// demoPatchMember changes the member in the demo as Discord would.
func (c *chatView) demoPatchMember(guildID discord.GuildID, id discord.UserID, body map[string]any) {
	if vs, err := c.st.Cabinet.VoiceState(guildID, id); err == nil {
		v := *vs
		if mute, ok := body["mute"].(bool); ok {
			v.Mute = mute
		}
		if deaf, ok := body["deaf"].(bool); ok {
			v.Deaf = deaf
		}
		if ch, ok := body["channel_id"]; ok {
			if chID, ok := ch.(discord.ChannelID); ok {
				v.ChannelID = chID
				c.st.Cabinet.VoiceStateSet(guildID, &v, true)
			} else {
				c.st.Cabinet.VoiceStateRemove(guildID, id)
			}
		} else {
			c.st.Cabinet.VoiceStateSet(guildID, &v, true)
		}
	}
	if m, err := c.st.Cabinet.Member(guildID, id); err == nil {
		mm := *m
		if nick, ok := body["nick"].(string); ok {
			mm.Nick = nick
		}
		if until, ok := body["communication_disabled_until"]; ok {
			mm.CommunicationDisabledUntil = discord.Timestamp{}
			if s, ok := until.(string); ok {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					mm.CommunicationDisabledUntil = discord.NewTimestamp(t)
				}
			}
		}
		c.st.Cabinet.MemberSet(guildID, &mm, true)
	}
	c.rebuildRows()
}

// setRole gives role to the member, or takes it away.
func (c *chatView) setRole(guildID discord.GuildID, id discord.UserID, role discord.Role, give bool) {
	// The box is ticked at once; the gateway confirms it.
	if m, err := c.st.Cabinet.Member(guildID, id); err == nil {
		mm := *m
		mm.RoleIDs = slices.DeleteFunc(slices.Clone(m.RoleIDs), func(r discord.RoleID) bool { return r == role.ID })
		if give {
			mm.RoleIDs = append(mm.RoleIDs, role.ID)
		}
		c.st.Cabinet.MemberSet(guildID, &mm, true)
	}
	if c.demo {
		return
	}
	method := "PUT"
	if !give {
		method = "DELETE"
	}
	path := api.EndpointGuilds + guildID.String() + "/members/" + id.String() + "/roles/" + role.ID.String()
	go func() {
		if err := c.st.Client.FastRequest(method, path); err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not change the roles", err) })
		}
	}()
}

// startCall opens the direct messages with the user and calls them, which
// rings them.
func (c *chatView) startCall(id discord.UserID) {
	if c.demo {
		c.u.voiceInfo("Calls are not made in the demo")
		return
	}
	go func() {
		ch, err := c.st.CreatePrivateChannel(id)
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not start the call", err)
				return
			}
			if c.selGuild.IsValid() {
				c.selectGuild(discord.NullGuildID)
			}
			c.rebuildRows()
			c.selectChannel(*ch)
			c.joinVoice(*ch)
		})
	}()
}

// sendInvite sends the user an invite to the channel chID, in their direct
// messages, as Discord's menu does.
func (c *chatView) sendInvite(chID discord.ChannelID, user discord.User) {
	if c.demo {
		c.u.voiceInfo("Invites are not sent in the demo")
		return
	}
	go func() {
		inv, err := c.st.CreateInvite(chID, api.CreateInviteData{})
		if err == nil {
			var dm *discord.Channel
			if dm, err = c.st.CreatePrivateChannel(user.ID); err == nil {
				_, err = c.st.SendMessage(dm.ID, "https://discord.gg/"+inv.Code)
			}
		}
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not send the invite", err)
				return
			}
			c.u.voiceInfo("Invite sent to " + user.DisplayOrUsername())
		})
	}()
}

// setFriend asks the user to be a friend, or accepts their request, if add,
// and otherwise removes the friend or the request.
func (c *chatView) setFriend(user discord.User, add bool) {
	if c.demo {
		f := &c.friends
		f.demoRels = slices.DeleteFunc(f.demoRels, func(r discord.Relationship) bool { return r.UserID == user.ID })
		if add {
			f.demoRels = append(f.demoRels, discord.Relationship{UserID: user.ID, User: user, Type: discord.SentFriendRequest})
		}
		return
	}
	incoming := c.relationship(user.ID) == discord.IncomingFriendRequest
	go func() {
		var err error
		switch {
		case add && incoming:
			err = c.st.Client.SetRelationship(user.ID, discord.FriendRelationship)
		case add:
			err = c.st.Client.FastRequest("PUT", api.EndpointMe+"/relationships/"+user.ID.String(), httputil.WithJSONBody(map[string]any{}))
		default:
			err = c.st.Client.DeleteRelationship(user.ID)
		}
		if err != nil {
			slog.Error("failed to change relationship", "err", err, "user_id", user.ID)
			msg := friendError(err)
			c.u.post(func() { c.u.voiceFailed("Could not change the friendship", fmt.Errorf("%s", msg)) })
		}
	}()
}

// setBlocked blocks or unblocks the user.
func (c *chatView) setBlocked(user discord.User, block bool) {
	if c.demo {
		f := &c.friends
		f.demoRels = slices.DeleteFunc(f.demoRels, func(r discord.Relationship) bool { return r.UserID == user.ID })
		if block {
			f.demoRels = append(f.demoRels, discord.Relationship{UserID: user.ID, User: user, Type: discord.BlockedRelationship})
		}
		return
	}
	go func() {
		var err error
		if block {
			err = c.st.Client.SetRelationship(user.ID, discord.BlockedRelationship)
		} else {
			err = c.st.Client.DeleteRelationship(user.ID)
		}
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not change the block", err) })
		}
	}()
}

// promptField is a text box of a form of the app.
func promptField(lbl, value, placeholder string, maxLength int, paragraph bool) *modalField {
	f := &modalField{label: lbl, text: true, paragraph: paragraph, maxLength: maxLength, placeholder: placeholder}
	f.ed.SingleLine = !paragraph
	f.ed.SetText(value)
	return f
}

// prompt opens a form of the app, which gives fn what was filled in.
func (c *chatView) prompt(title, submit string, danger bool, texts []string, fields []*modalField, fn func(values []string)) {
	mv := &modalView{ev: modalCreateEvent{Title: title}, fields: fields, texts: texts, local: fn, submitText: submit, danger: danger}
	mv.list.Axis = layout.Vertical
	c.closeMenu()
	c.inter.modal = mv
}

func (c *chatView) promptNote(user discord.User) {
	c.prompt("Note about "+user.DisplayOrUsername(), "Save", false, nil,
		[]*modalField{promptField("Note", c.note(user.ID), "Click to add a note", 256, true)},
		func(v []string) {
			note := strings.TrimSpace(v[0])
			if c.demo {
				c.demoNotes[user.ID] = note
				return
			}
			go func() {
				if err := c.st.Client.SetNote(user.ID, note); err != nil {
					c.u.post(func() { c.u.voiceFailed("Could not save the note", err) })
				}
			}()
		})
}

func (c *chatView) promptNickname(user discord.User, guildID discord.GuildID) {
	nick := ""
	if m, err := c.st.Cabinet.Member(guildID, user.ID); err == nil {
		nick = m.Nick
	}
	c.prompt("Change Nickname", "Save", false,
		[]string{"Leave it empty to go back to " + user.DisplayOrUsername() + "."},
		[]*modalField{promptField("Nickname", nick, user.DisplayOrUsername(), 32, false)},
		func(v []string) {
			c.patchMember("Could not change the nickname", guildID, user.ID, map[string]any{"nick": strings.TrimSpace(v[0])})
		})
}

func (c *chatView) promptKick(user discord.User, guildID discord.GuildID) {
	name := c.memberName(guildID, user.ID, nil)
	guild := "the server"
	if g, err := c.st.Cabinet.Guild(guildID); err == nil {
		guild = g.Name
	}
	c.prompt("Kick "+name+" from "+guild, "Kick", true,
		[]string{"They will be able to join again with a new invite."},
		[]*modalField{promptField("Reason for kick", "", "", 512, true)},
		func(v []string) {
			if c.demo {
				c.u.voiceInfo(name + " would be kicked")
				return
			}
			go func() {
				if err := c.st.Kick(guildID, user.ID, api.AuditLogReason(strings.TrimSpace(v[0]))); err != nil {
					c.u.post(func() { c.u.voiceFailed("Could not kick "+name, err) })
				}
			}()
		})
}

// banHistory are how far back the messages of a member banned can be
// deleted, as Discord offers it.
var banHistory = []discord.SelectOption{
	{Label: "Don't delete any", Value: "0"},
	{Label: "Previous hour", Value: "3600"},
	{Label: "Previous 6 hours", Value: "21600"},
	{Label: "Previous 12 hours", Value: "43200"},
	{Label: "Previous 24 hours", Value: "86400"},
	{Label: "Previous 3 days", Value: "259200"},
	{Label: "Previous 7 days", Value: "604800"},
}

func (c *chatView) promptBan(user discord.User, guildID discord.GuildID) {
	name := c.memberName(guildID, user.ID, nil)
	history := &modalField{label: "Delete message history", options: slices.Clone(banHistory), chosen: 0}
	history.optClick = make([]widget.Clickable, len(history.options))
	c.prompt("Ban "+name+"?", "Ban", true, nil,
		[]*modalField{history, promptField("Reason for ban", "", "", 512, true)},
		func(v []string) {
			if c.demo {
				c.u.voiceInfo(name + " would be banned")
				return
			}
			secs, _ := strconv.Atoi(v[0])
			opts := []httputil.RequestOption{httputil.WithJSONBody(map[string]any{"delete_message_seconds": secs})}
			if reason := strings.TrimSpace(v[1]); reason != "" {
				opts = append(opts, httputil.WithHeaders(http.Header{"X-Audit-Log-Reason": {url.PathEscape(reason)}}))
			}
			path := api.EndpointGuilds + guildID.String() + "/bans/" + user.ID.String()
			go func() {
				if err := c.st.Client.FastRequest("PUT", path, opts...); err != nil {
					c.u.post(func() { c.u.voiceFailed("Could not ban "+name, err) })
				}
			}()
		})
}

// voiceRowMenu watches a right click on a member of a voice channel in the
// channel list, which opens their menu.
func (c *chatView) voiceRowMenu(gtx layout.Context, r channelRow, size image.Point) {
	if rightClicked(gtx, c.rightTag("vm"+r.user.String()), size) {
		c.openUserMenu(r.user, r.ch.GuildID, r.ch.ID)
	}
}

// layoutSoundboardVolumeItem is the slider of how loud the soundboard
// sounds of the user of the menu play, from silent to twice as loud.
func (c *chatView) layoutSoundboardVolumeItem(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	um := &c.umenu
	id := um.user.ID
	if r := um.soundboard.update(gtx); r.changed || r.released {
		c.u.setSoundboardVolume(id, r.value*2)
		if r.released {
			c.u.saveAppSettings()
		}
	}
	vol := c.u.soundboardVolume(id)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 14, colorText, "Soundboard Volume").Layout),
				layout.Rigid(label(th, 12, colorMuted, fmt.Sprintf("%d%%", int(vol*100+0.5))).Layout),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return um.soundboard.layout(gtx, 0, vol/2)
		}),
	)
}
