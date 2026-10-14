package gui

import (
	"fmt"
	"slices"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
)

// The pages of a channel's menu.
const (
	chanMenuMain = iota
	chanMenuMute
	chanMenuNotify
	chanMenuMove
)

// channelMenu is the menu of a channel, opened with the right button, as
// Discord's.
type channelMenu struct {
	ch   discord.Channel
	page int
	// overrides are the channels' notification settings changed here,
	// shown before Discord says so.
	overrides map[discord.ChannelID]gateway.UserChannelOverride
	// The buttons of a channel's row: invite, and its settings.
	invite, gear map[discord.ChannelID]*widget.Clickable
}

// openChannelMenu opens the menu of the channel at the pointer.
func (c *chatView) openChannelMenu(ch discord.Channel) {
	c.chmenu.ch, c.chmenu.page = ch, chanMenuMain
	c.openMenu(c.pointerAt, 240, c.channelMenuItems)
}

// channelOverride is the user's notification settings for the channel.
func (c *chatView) channelOverride(id discord.ChannelID) gateway.UserChannelOverride {
	if o, ok := c.chmenu.overrides[id]; ok {
		return o
	}
	if c.st.MutedState == nil {
		return gateway.UserChannelOverride{ChannelID: id, Notifications: gateway.GuildDefaults}
	}
	return c.st.MutedState.ChannelOverrides(id)
}

// patchChannelOverride changes the user's notification settings for the
// channel.
func (c *chatView) patchChannelOverride(ch discord.Channel, change func(*gateway.UserChannelOverride), body map[string]any) {
	m := &c.chmenu
	if m.overrides == nil {
		m.overrides = map[discord.ChannelID]gateway.UserChannelOverride{}
	}
	o := c.channelOverride(ch.ID)
	change(&o)
	m.overrides[ch.ID] = o
	c.menu.stay = true
	if c.demo {
		return
	}
	payload := map[string]any{"channel_overrides": map[string]any{ch.ID.String(): body}}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointMe+"/guilds/"+ch.GuildID.String()+"/settings", httputil.WithJSONBody(payload))
		c.u.post(func() {
			if err != nil {
				c.u.voiceFailed("Could not change the channel's notifications", err)
				return
			}
			c.rebuildRows()
		})
	}()
}

// channelMuted reports whether the channel is muted, as changed here or as
// Discord says.
func (c *chatView) channelMuted(id discord.ChannelID) bool {
	if o, ok := c.chmenu.overrides[id]; ok {
		return o.Muted && (o.MuteConfig == nil || !o.MuteConfig.EndTime.IsValid() || o.MuteConfig.EndTime.Time().After(time.Now()))
	}
	return c.st.MutedState != nil && c.st.MutedState.Channel(id)
}

// muteTimes are how long a channel is muted for, as Discord offers them.
var muteTimes = []struct {
	name string
	for_ time.Duration
}{
	{"For 15 Minutes", 15 * time.Minute}, {"For 1 Hour", time.Hour}, {"For 3 Hours", 3 * time.Hour},
	{"For 8 Hours", 8 * time.Hour}, {"For 24 Hours", 24 * time.Hour}, {"Until I turn it back on", 0},
}

func (c *chatView) channelMenuItems() []menuItem {
	m := &c.chmenu
	ch := m.ch
	page := func(p int) func() {
		return func() {
			m.page = p
			c.menu.stay = true
			c.menu.list.Position = layout.Position{}
		}
	}
	back := menuItem{key: "back", text: "Back", back: true, act: page(chanMenuMain)}
	switch m.page {
	case chanMenuMute:
		items := []menuItem{back, {title: true, text: "Mute Channel"}}
		for _, t := range muteTimes {
			t := t
			items = append(items, menuItem{key: t.name, text: t.name, act: func() {
				config := map[string]any{"selected_time_window": -1, "end_time": nil}
				var end discord.Timestamp
				if t.for_ > 0 {
					until := time.Now().Add(t.for_)
					end = discord.NewTimestamp(until)
					config = map[string]any{"selected_time_window": int(t.for_.Seconds()), "end_time": until.UTC().Format(time.RFC3339)}
				}
				c.patchChannelOverride(ch, func(o *gateway.UserChannelOverride) {
					o.Muted, o.MuteConfig = true, &gateway.UserMuteConfig{SelectedTimeWindow: int(t.for_.Seconds()), EndTime: end}
				}, map[string]any{"muted": true, "mute_config": config})
				c.menu.stay = false
			}})
		}
		return items
	case chanMenuNotify:
		o := c.channelOverride(ch.ID)
		level := func(text string, n gateway.UserNotification) menuItem {
			return menuItem{key: "n" + text, text: text, check: checked(o.Notifications == n), act: func() {
				c.patchChannelOverride(ch, func(o *gateway.UserChannelOverride) { o.Notifications = n }, map[string]any{"message_notifications": n})
			}}
		}
		return []menuItem{back, {title: true, text: "Notification Settings"},
			level("Use Category Default", gateway.GuildDefaults),
			level("All Messages", gateway.AllNotifications),
			level("Only @mentions", gateway.OnlyMentions),
			level("Nothing", gateway.NoNotifications),
		}
	case chanMenuMove:
		items := []menuItem{back, {title: true, text: "Move To"}}
		cats := c.guildCategories(ch.GuildID)
		items = append(items, menuItem{key: "nocat", text: "No Category", check: checked(!ch.ParentID.IsValid()), act: func() { c.moveChannel(ch, 0) }})
		for _, cat := range cats {
			cat := cat
			items = append(items, menuItem{key: "cat" + cat.ID.String(), text: cat.Name, check: checked(ch.ParentID == cat.ID), act: func() { c.moveChannel(ch, cat.ID) }})
		}
		return items
	}

	perms := c.myPerms(ch.ID)
	manage := perms.Has(discord.PermissionManageChannels)
	category := ch.Type == discord.GuildCategory
	unread := !category && c.channelUnread(ch.ID) != ningen.ChannelRead
	markCol := colorMuted
	if unread {
		markCol = colorText
	}
	if category {
		// A category is read when its channels are.
		for _, child := range c.categoryChildren(ch) {
			if c.channelUnread(child.ID) != ningen.ChannelRead {
				unread, markCol = true, colorText
			}
		}
	}
	items := []menuItem{{key: "read", text: "Mark As Read", col: markCol, act: func() {
		if !unread {
			return
		}
		if category {
			for _, child := range c.categoryChildren(ch) {
				c.markRead(child.ID)
			}
			return
		}
		c.markRead(ch.ID)
	}}, {sep: true}}
	if category {
		items = append(items,
			menuItem{key: "collapse", text: "Collapse Category", check: checked(c.collapsed[ch.ID]), act: func() {
				c.collapsed[ch.ID] = !c.collapsed[ch.ID]
				c.rebuildRows()
				c.menu.stay = true
			}},
			menuItem{text: "Collapse All Categories", act: func() {
				for _, cat := range c.guildCategories(ch.GuildID) {
					c.collapsed[cat.ID] = true
				}
				c.rebuildRows()
			}},
		)
	}
	voice := isVoiceKind(ch.Type)
	if !category {
		if perms.Has(discord.PermissionCreateInstantInvite) {
			invite := "Invite to Channel"
			if voice {
				invite = "Invite to Voice Chat"
			}
			items = append(items, menuItem{text: invite, col: colorAccent, act: func() { c.copyInvite(ch.ID) }})
		}
		pinned := slices.Contains(c.u.app.PinnedChannels, ch.ID)
		pinText := "Pin Channel to Top"
		if pinned {
			pinText = "Unpin Channel"
		}
		items = append(items,
			menuItem{key: "pin", text: pinText, act: func() {
				a := &c.u.app
				a.PinnedChannels = slices.DeleteFunc(a.PinnedChannels, func(id discord.ChannelID) bool { return id == ch.ID })
				if !pinned {
					a.PinnedChannels = append(a.PinnedChannels, ch.ID)
				}
				c.u.saveAppSettings()
				c.rebuildRows()
			}},
			menuItem{text: "Copy Link", act: func() {
				c.copyText = fmt.Sprintf("https://discord.com/channels/%s/%s", ch.GuildID, ch.ID)
			}},
		)
	}
	if voice {
		items = append(items, menuItem{sep: true}, menuItem{text: "Open Chat", act: func() { c.selectChannel(ch) }})
		if c.voiceStatuses[ch.ID] != "" && (manage || perms.Has(discord.Permissions(1)<<48)) {
			items = append(items, menuItem{text: "Clear Channel Status", act: func() { c.clearVoiceStatus(ch.ID) }})
		}
		hidden := slices.Contains(c.u.app.HiddenVoiceNames, ch.ID)
		items = append(items, menuItem{key: "hide", text: "Hide Names", check: checked(hidden), act: func() {
			a := &c.u.app
			a.HiddenVoiceNames = slices.DeleteFunc(a.HiddenVoiceNames, func(id discord.ChannelID) bool { return id == ch.ID })
			if !hidden {
				a.HiddenVoiceNames = append(a.HiddenVoiceNames, ch.ID)
			}
			c.u.saveAppSettings()
			c.menu.stay = true
		}})
	}
	items = append(items, menuItem{sep: true})
	if c.channelMuted(ch.ID) {
		items = append(items, menuItem{key: "unmute", text: "Unmute " + kindName(ch), act: func() {
			c.patchChannelOverride(ch, func(o *gateway.UserChannelOverride) { o.Muted, o.MuteConfig = false, nil }, map[string]any{"muted": false})
			c.menu.stay = false
		}})
	} else {
		items = append(items, menuItem{key: "mute", text: "Mute " + kindName(ch), sub: true, act: page(chanMenuMute)})
	}
	if !voice {
		items = append(items, menuItem{key: "notify", text: "Notification Settings", sub: true, act: page(chanMenuNotify)})
	}
	if manage && !category {
		items = append(items, menuItem{sep: true}, menuItem{key: "move", text: "Move To…", sub: true, act: page(chanMenuMove)})
	}
	if c.canEditChannel(ch) || manage {
		items = append(items, menuItem{sep: true})
		edit := "Edit Channel"
		if category {
			edit = "Edit Category"
		}
		if c.canEditChannel(ch) {
			items = append(items, menuItem{text: edit, act: func() { c.openChannelSettings(ch.ID) }})
		}
		if manage {
			if !category {
				items = append(items, menuItem{text: "Duplicate Channel", act: func() { c.duplicateChannel(ch) }})
			}
			create := "Create Text Channel"
			if voice {
				create = "Create Voice Channel"
			}
			if !category {
				items = append(items, menuItem{text: create, act: func() {
					c.openCreateChannel(ch.GuildID, false)
					if voice {
						c.newChannel.kind = discord.GuildVoice
					}
					c.newChannel.parent = ch.ParentID
				}})
			}
			items = append(items,
				menuItem{key: "delete", text: "Delete " + map[bool]string{true: "Category", false: "Channel"}[category], col: colorRed, act: func() { c.askDeleteChannel(ch) }},
			)
		}
	}
	items = append(items, menuItem{sep: true}, menuItem{text: "Copy " + kindName(ch) + " ID", lead: menuIcon(iconCopy, colorText), act: func() { c.copyText = ch.ID.String() }})
	return items
}

// kindName is what Discord's menus call the channel: a channel, or a
// category.
func kindName(ch discord.Channel) string {
	if ch.Type == discord.GuildCategory {
		return "Category"
	}
	return "Channel"
}

// categoryChildren are the channels in the category.
func (c *chatView) categoryChildren(cat discord.Channel) []discord.Channel {
	channels, _ := c.st.Cabinet.Channels(cat.GuildID)
	var out []discord.Channel
	for _, ch := range channels {
		if ch.ParentID == cat.ID {
			out = append(out, ch)
		}
	}
	return out
}

// guildCategories are the server's categories, by position.
func (c *chatView) guildCategories(guildID discord.GuildID) []discord.Channel {
	channels, _ := c.st.Cabinet.Channels(guildID)
	var out []discord.Channel
	for _, ch := range channels {
		if ch.Type == discord.GuildCategory {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b discord.Channel) int { return a.Position - b.Position })
	return out
}

// moveChannel puts the channel in the category, or in none.
func (c *chatView) moveChannel(ch discord.Channel, parent discord.ChannelID) {
	if c.demo {
		ch.ParentID = parent
		c.st.Cabinet.ChannelSet(&ch, true)
		c.rebuildRows()
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointChannels+ch.ID.String(), httputil.WithJSONBody(map[string]any{"parent_id": nullID(discord.Snowflake(parent))}))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not move the channel", err) })
		}
	}()
}

// duplicateChannel makes a channel as the one given, named the same, in
// its category with its permissions.
func (c *chatView) duplicateChannel(ch discord.Channel) {
	body := map[string]any{
		"name": ch.Name, "type": ch.Type, "topic": ch.Topic, "nsfw": ch.NSFW,
		"rate_limit_per_user": ch.UserRateLimit, "permission_overwrites": ch.Overwrites,
		"parent_id": nullID(discord.Snowflake(ch.ParentID)),
	}
	if isVoiceKind(ch.Type) {
		body["bitrate"], body["user_limit"] = ch.VoiceBitrate, ch.VoiceUserLimit
	}
	if c.demo {
		dup := ch
		dup.ID = discord.ChannelID(discord.NewSnowflake(time.Now()))
		c.st.Cabinet.ChannelSet(&dup, false)
		c.rebuildRows()
		return
	}
	go func() {
		err := c.st.Client.FastRequest("POST", api.EndpointGuilds+ch.GuildID.String()+"/channels", httputil.WithJSONBody(body))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not duplicate the channel", err) })
		}
	}()
}

// clearVoiceStatus takes away what the voice channel says it is about.
func (c *chatView) clearVoiceStatus(id discord.ChannelID) {
	c.setVoiceStatus(id, "")
	if c.demo {
		return
	}
	go func() {
		err := c.st.Client.FastRequest("PUT", api.EndpointChannels+id.String()+"/voice-status", httputil.WithJSONBody(map[string]any{"status": ""}))
		if err != nil {
			c.u.post(func() { c.u.voiceFailed("Could not clear the channel's status", err) })
		}
	}()
}

// updateChannelButtons handles the buttons of the channels' rows.
func (c *chatView) updateChannelButtons(gtx layout.Context) {
	m := &c.chmenu
	for id, cl := range m.invite {
		if cl.Clicked(gtx) {
			c.copyInvite(id)
		}
	}
	for id, cl := range m.gear {
		if cl.Clicked(gtx) {
			c.openChannelSettings(id)
		}
	}
}
