package gui

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"image"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ncruces/zenity"
)

// The pages of a channel's settings, as Discord's.
const (
	csOverview = iota
	csPermissions
	csInvites
	csIntegrations
	csPages
)

var csPageNames = [csPages]string{"Overview", "Permissions", "Invites", "Integrations"}

// channelSettings is the screen of a channel's settings, over the window.
type channelSettings struct {
	open, focused bool
	// ch is the channel as last saved, changed here before Discord says.
	ch       discord.Channel
	page     int
	pages    [csPages]widget.Clickable
	del      widget.Clickable
	list     widget.List
	closeBtn widget.Clickable
	err      string
	info     string

	// Overview.
	name, topic         widget.Editor
	origName, origTopic string
	save, reset         widget.Clickable
	slowBtn, archiveBtn widget.Clickable
	bitrate, limit      slider
	videoQ              [2]widget.Clickable
	regionBtn           widget.Clickable
	visibility          [2]widget.Clickable

	// Permissions: target is the role, or member, whose permissions show.
	target                 discord.Snowflake
	targets                map[discord.Snowflake]*widget.Clickable
	addTarget, dropTarget  widget.Clickable
	syncBtn, privateBtn    widget.Clickable
	perms                  map[uint]*[3]widget.Clickable
	permSearch             widget.Editor
	invites                fetched[[]gsInvite]
	createInvite, pause    widget.Clickable
	revoke                 map[string]*widget.Clickable
	hooks                  fetched[[]webhook]
	newHook                widget.Clickable
	hookOpen               map[string]bool
	hookToggle, hookCopy   map[string]*widget.Clickable
	hookDelete, hookAvatar map[string]*widget.Clickable
	hookChan, hookSave     map[string]*widget.Clickable
	hookName               map[string]*widget.Editor
}

// webhook is a channel's webhook, or a channel it follows.
type webhook struct {
	ID        string            `json:"id"`
	Type      int               `json:"type"`
	Name      string            `json:"name"`
	Avatar    string            `json:"avatar"`
	Token     string            `json:"token"`
	ChannelID discord.ChannelID `json:"channel_id"`
	User      *discord.User     `json:"user"`
}

// canEditChannel reports whether the user may change the channel.
func (c *chatView) canEditChannel(ch discord.Channel) bool {
	p := c.myPerms(ch.ID)
	return p.Has(discord.PermissionManageChannels) || p.Has(discord.PermissionManageRoles)
}

// openChannelSettings opens the settings of the channel.
func (c *chatView) openChannelSettings(id discord.ChannelID) {
	ch, err := c.st.Cabinet.Channel(id)
	if err != nil {
		return
	}
	c.closeMenu()
	c.profile = nil
	cs := &c.cset
	*cs = channelSettings{open: true, ch: *ch, target: discord.Snowflake(ch.GuildID)}
	cs.list.Axis = layout.Vertical
	cs.name.SingleLine = true
	cs.origName, cs.origTopic = ch.Name, ch.Topic
	cs.name.SetText(ch.Name)
	cs.topic.SetText(ch.Topic)
	cs.topic.MaxLen = 1024
}

func (c *chatView) showChannelPage(p int) {
	cs := &c.cset
	cs.page, cs.err, cs.info = p, "", ""
	cs.list.Position = layout.Position{}
	id := cs.ch.ID.String()
	switch p {
	case csInvites:
		cs.invites.loaded = false
		fetch(c, &cs.invites, "the invites", api.EndpointChannels+id+"/invites", func() []gsInvite { return nil })
	case csIntegrations:
		cs.hooks.loaded = false
		fetch(c, &cs.hooks, "the webhooks", api.EndpointChannels+id+"/webhooks", func() []webhook { return nil })
	}
}

// channelRequest sends Discord a change of the channel, and calls done once
// it took it.
func (c *chatView) channelRequest(method, endpoint string, body any, done func()) {
	cs := &c.cset
	cs.err, cs.info = "", ""
	if c.demo {
		cs.info = "Saved."
		if done != nil {
			done()
		}
		return
	}
	id := cs.ch.ID
	go func() {
		var opts []httputil.RequestOption
		if body != nil {
			opts = append(opts, httputil.WithJSONBody(body))
		}
		err := c.st.Client.FastRequest(method, endpoint, opts...)
		c.u.post(func() {
			if cs.ch.ID != id {
				return
			}
			if err != nil {
				cs.err = "Discord did not take the change: " + err.Error()
				return
			}
			cs.info = "Saved."
			if done != nil {
				done()
			}
		})
	}()
}

// patchChannel changes the channel's fields: apply shows them at once.
func (c *chatView) patchChannel(apply func(*discord.Channel), body map[string]any) {
	cs := &c.cset
	apply(&cs.ch)
	c.channelRequest("PATCH", api.EndpointChannels+cs.ch.ID.String(), body, nil)
}

// voiceRegions are where voice can be served from, as Discord offers them.
var voiceRegions = []struct{ id, name string }{
	{"", "Automatic"}, {"brazil", "Brazil"}, {"hongkong", "Hong Kong"}, {"india", "India"}, {"japan", "Japan"},
	{"rotterdam", "Rotterdam"}, {"singapore", "Singapore"}, {"southafrica", "South Africa"}, {"sydney", "Sydney"},
	{"us-central", "US Central"}, {"us-east", "US East"}, {"us-south", "US South"}, {"us-west", "US West"},
}

func regionName(id string) string {
	for _, r := range voiceRegions {
		if r.id == id {
			return r.name
		}
	}
	return id
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// maxBitrate is the highest bitrate, in bits, the server's boosts allow.
func (c *chatView) maxBitrate() uint {
	g, err := c.st.Cabinet.Guild(c.cset.ch.GuildID)
	if err != nil {
		return 96000
	}
	return [...]uint{96000, 128000, 256000, 384000}[min(int(g.NitroBoost), 3)]
}

// bitrateAt is the bitrate at v along the slider, in whole kbps.
func (c *chatView) bitrateAt(v float32) uint {
	low, high := float32(8000), float32(c.maxBitrate())
	return uint(low+v*(high-low)) / 1000 * 1000
}

// isVoiceKind reports whether the channel has voice.
func isVoiceKind(t discord.ChannelType) bool {
	return t == discord.GuildVoice || t == discord.GuildStageVoice
}

// slowmodes are the waits of slowmode, as Discord offers them.
var slowmodes = []discord.Seconds{0, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 21600}

func slowmodeName(s discord.Seconds) string {
	switch {
	case s == 0:
		return "Off"
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%dh", s/3600)
}

// archiveTimes are when threads hide after no activity, in minutes.
var archiveTimes = []struct {
	min  discord.ArchiveDuration
	name string
}{{60, "1 Hour"}, {1440, "24 Hours"}, {4320, "3 Days"}, {10080, "1 Week"}}

func archiveName(d discord.ArchiveDuration) string {
	for _, a := range archiveTimes {
		if a.min == d {
			return a.name
		}
	}
	return "3 Days"
}

func (c *chatView) updateChannelSettings(gtx layout.Context) {
	cs := &c.cset
	if !cs.focused {
		gtx.Execute(key.FocusCmd{})
		cs.focused = true
	}
	for {
		e, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := e.(key.Event); ok && e.State == key.Press {
			cs.open = false
			return
		}
	}
	if cs.closeBtn.Clicked(gtx) {
		cs.open = false
		return
	}
	for p := range cs.pages {
		if cs.pages[p].Clicked(gtx) && cs.page != p {
			c.showChannelPage(p)
		}
	}
	ch := &cs.ch
	id := ch.ID.String()
	if cs.del.Clicked(gtx) {
		c.askDeleteChannel(*ch)
	}

	// Overview.
	if cs.save.Clicked(gtx) {
		name, topic := strings.TrimSpace(cs.name.Text()), strings.TrimSpace(cs.topic.Text())
		body := map[string]any{}
		if name != cs.origName && name != "" {
			body["name"] = name
		}
		if topic != cs.origTopic {
			body["topic"] = topic
		}
		if len(body) > 0 {
			c.patchChannel(func(ch *discord.Channel) { ch.Name, ch.Topic = name, topic }, body)
			cs.origName, cs.origTopic = name, topic
		}
	}
	if cs.reset.Clicked(gtx) {
		cs.name.SetText(cs.origName)
		cs.topic.SetText(cs.origTopic)
	}
	if cs.slowBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 200, func() []menuItem {
			var items []menuItem
			for _, s := range slowmodes {
				s := s
				items = append(items, menuItem{key: fmt.Sprint("slow", s), text: slowmodeName(s), check: checked(ch.UserRateLimit == s), act: func() {
					c.patchChannel(func(ch *discord.Channel) { ch.UserRateLimit = s }, map[string]any{"rate_limit_per_user": s})
				}})
			}
			return items
		})
	}
	if cs.archiveBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 200, func() []menuItem {
			var items []menuItem
			for _, a := range archiveTimes {
				a := a
				items = append(items, menuItem{key: a.name, text: a.name, check: checked(ch.DefaultAutoArchiveDuration == a.min), act: func() {
					c.patchChannel(func(ch *discord.Channel) { ch.DefaultAutoArchiveDuration = a.min }, map[string]any{"default_auto_archive_duration": a.min})
				}})
			}
			return items
		})
	}
	for i := range cs.visibility {
		if cs.visibility[i].Clicked(gtx) {
			nsfw := i == 1
			c.patchChannel(func(ch *discord.Channel) { ch.NSFW = nsfw }, map[string]any{"nsfw": nsfw})
		}
	}
	if r := cs.bitrate.update(gtx); r.released {
		b := c.bitrateAt(r.value)
		c.patchChannel(func(ch *discord.Channel) { ch.VoiceBitrate = b }, map[string]any{"bitrate": b})
	}
	if r := cs.limit.update(gtx); r.released {
		n := uint(r.value*99 + 0.5)
		c.patchChannel(func(ch *discord.Channel) { ch.VoiceUserLimit = n }, map[string]any{"user_limit": n})
	}
	for i := range cs.videoQ {
		if cs.videoQ[i].Clicked(gtx) {
			mode := discord.VideoQualityMode(i + 1)
			c.patchChannel(func(ch *discord.Channel) { ch.VideoQualityMode = mode }, map[string]any{"video_quality_mode": mode})
		}
	}
	if cs.regionBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 220, func() []menuItem {
			var items []menuItem
			for _, r := range voiceRegions {
				r := r
				items = append(items, menuItem{key: "rg" + r.id, text: r.name, check: checked(ch.RTCRegionID == r.id), act: func() {
					c.patchChannel(func(ch *discord.Channel) { ch.RTCRegionID = r.id }, map[string]any{"rtc_region": nullString(r.id)})
				}})
			}
			return items
		})
	}

	// Permissions.
	for t, click := range cs.targets {
		if click.Clicked(gtx) {
			cs.target = t
		}
	}
	if cs.addTarget.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, c.overwriteChoices)
	}
	if cs.dropTarget.Clicked(gtx) && cs.target != discord.Snowflake(ch.GuildID) {
		t := cs.target
		c.channelRequest("DELETE", api.EndpointChannels+id+"/permissions/"+t.String(), nil, func() {
			ch.Overwrites = slices.DeleteFunc(ch.Overwrites, func(o discord.Overwrite) bool { return o.ID == t })
			cs.target = discord.Snowflake(ch.GuildID)
		})
	}
	if cs.privateBtn.Clicked(gtx) {
		ow := c.overwrite(discord.Snowflake(ch.GuildID))
		if ow.Deny.Has(discord.PermissionViewChannel) {
			ow.Deny &^= discord.PermissionViewChannel
		} else {
			ow.Deny |= discord.PermissionViewChannel
			ow.Allow &^= discord.PermissionViewChannel
		}
		c.putOverwrite(ow)
	}
	if cs.syncBtn.Clicked(gtx) {
		if parent, err := c.st.Cabinet.Channel(ch.ParentID); err == nil {
			overwrites := slices.Clone(parent.Overwrites)
			if overwrites == nil {
				overwrites = []discord.Overwrite{}
			}
			c.patchChannel(func(ch *discord.Channel) { ch.Overwrites = overwrites }, map[string]any{"permission_overwrites": overwrites})
		}
	}
	for bit, clicks := range cs.perms {
		for state := range clicks {
			if !clicks[state].Clicked(gtx) {
				continue
			}
			ow := c.overwrite(cs.target)
			p := discord.Permissions(1) << bit
			ow.Allow &^= p
			ow.Deny &^= p
			switch state {
			case 0:
				ow.Deny |= p
			case 2:
				ow.Allow |= p
			}
			c.putOverwrite(ow)
		}
	}

	// Invites.
	if cs.createInvite.Clicked(gtx) {
		c.copyInvite(ch.ID)
		cs.invites.loaded = false
		c.reloadChannelPageLater(csInvites)
	}
	if cs.pause.Clicked(gtx) {
		l := &c.gset.lists
		var until any
		if !l.invitesPaused() {
			t := time.Now().Add(24 * time.Hour).UTC()
			until = t.Format(time.RFC3339)
			l.pausedUntil = &t
		} else {
			l.pausedUntil = nil
		}
		c.channelRequest("PUT", api.EndpointGuilds+ch.GuildID.String()+"/incident-actions", map[string]any{"invites_disabled_until": until, "dms_disabled_until": nil}, nil)
	}
	for code, click := range cs.revoke {
		if click.Clicked(gtx) {
			code := code
			c.channelRequest("DELETE", api.EndpointInvites+code, nil, func() {
				cs.invites.data = slices.DeleteFunc(cs.invites.data, func(i gsInvite) bool { return i.Code == code })
			})
		}
	}

	// Integrations.
	if cs.newHook.Clicked(gtx) {
		c.createWebhook()
	}
	for hid, click := range cs.hookToggle {
		if click.Clicked(gtx) {
			cs.hookOpen[hid] = !cs.hookOpen[hid]
		}
	}
	for hid, click := range cs.hookCopy {
		if click.Clicked(gtx) {
			for _, h := range cs.hooks.data {
				if h.ID == hid {
					c.copyText = "https://discord.com/api/webhooks/" + h.ID + "/" + h.Token
					cs.info = "Webhook URL copied."
				}
			}
		}
	}
	for hid, click := range cs.hookDelete {
		if click.Clicked(gtx) {
			hid := hid
			c.channelRequest("DELETE", api.EndpointWebhooks+hid, nil, func() {
				cs.hooks.data = slices.DeleteFunc(cs.hooks.data, func(h webhook) bool { return h.ID == hid })
			})
		}
	}
	for hid, click := range cs.hookSave {
		if click.Clicked(gtx) {
			hid := hid
			name := strings.TrimSpace(cs.hookName[hid].Text())
			if name != "" {
				c.channelRequest("PATCH", api.EndpointWebhooks+hid, map[string]any{"name": name}, func() {
					for i := range cs.hooks.data {
						if cs.hooks.data[i].ID == hid {
							cs.hooks.data[i].Name = name
						}
					}
				})
			}
		}
	}
	for hid, click := range cs.hookChan {
		if click.Clicked(gtx) {
			hid := hid
			c.openMenu(c.pointerAt, 260, func() []menuItem {
				var items []menuItem
				channels, _ := c.st.Cabinet.Channels(ch.GuildID)
				slices.SortFunc(channels, func(a, b discord.Channel) int { return a.Position - b.Position })
				for _, other := range channels {
					if other.Type != discord.GuildText && other.Type != discord.GuildAnnouncement {
						continue
					}
					other := other
					items = append(items, menuItem{key: "hc" + other.ID.String(), text: "# " + other.Name, act: func() {
						c.channelRequest("PATCH", api.EndpointWebhooks+hid, map[string]any{"channel_id": other.ID.String()}, func() {
							// It posts elsewhere now, and leaves this list.
							cs.hooks.data = slices.DeleteFunc(cs.hooks.data, func(h webhook) bool { return h.ID == hid && other.ID != ch.ID })
						})
					}})
				}
				return items
			})
		}
	}
	for hid, click := range cs.hookAvatar {
		if click.Clicked(gtx) {
			c.pickWebhookAvatar(hid)
		}
	}
}

// overwrite is the channel's overwrite of the role or member id, empty if
// it has none.
func (c *chatView) overwrite(id discord.Snowflake) discord.Overwrite {
	cs := &c.cset
	for _, o := range cs.ch.Overwrites {
		if o.ID == id {
			return o
		}
	}
	typ := discord.OverwriteMember
	if _, err := c.st.Cabinet.Role(cs.ch.GuildID, discord.RoleID(id)); err == nil || id == discord.Snowflake(cs.ch.GuildID) {
		typ = discord.OverwriteRole
	}
	return discord.Overwrite{ID: id, Type: typ}
}

// putOverwrite sends the overwrite to Discord, and shows it at once.
func (c *chatView) putOverwrite(ow discord.Overwrite) {
	cs := &c.cset
	if i := slices.IndexFunc(cs.ch.Overwrites, func(o discord.Overwrite) bool { return o.ID == ow.ID }); i >= 0 {
		cs.ch.Overwrites[i] = ow
	} else {
		cs.ch.Overwrites = append(cs.ch.Overwrites, ow)
	}
	body := map[string]any{"type": ow.Type, "allow": strconv.FormatUint(uint64(ow.Allow), 10), "deny": strconv.FormatUint(uint64(ow.Deny), 10)}
	c.channelRequest("PUT", api.EndpointChannels+cs.ch.ID.String()+"/permissions/"+ow.ID.String(), body, nil)
}

// overwriteChoices are the roles and members to give the channel's own
// permissions.
func (c *chatView) overwriteChoices() []menuItem {
	cs := &c.cset
	has := func(id discord.Snowflake) bool {
		return slices.ContainsFunc(cs.ch.Overwrites, func(o discord.Overwrite) bool { return o.ID == id })
	}
	items := []menuItem{{title: true, text: "Roles"}}
	roles, _ := c.st.Cabinet.Roles(cs.ch.GuildID)
	slices.SortFunc(roles, func(a, b discord.Role) int { return b.Position - a.Position })
	for _, r := range roles {
		if r.ID == discord.RoleID(cs.ch.GuildID) || has(discord.Snowflake(r.ID)) {
			continue
		}
		r := r
		col := colorOffline
		if r.Color != 0 {
			col = rgb(uint32(r.Color))
		}
		items = append(items, menuItem{key: "r" + r.ID.String(), text: r.Name, lead: func(gtx layout.Context) layout.Dimensions { return dot(gtx, col, 10) }, act: func() {
			ow := discord.Overwrite{ID: discord.Snowflake(r.ID), Type: discord.OverwriteRole}
			c.putOverwrite(ow)
			cs.target = ow.ID
		}})
	}
	items = append(items, menuItem{sep: true}, menuItem{title: true, text: "Members"})
	members, _ := c.st.Cabinet.Members(cs.ch.GuildID)
	n := 0
	for _, m := range members {
		if has(discord.Snowflake(m.User.ID)) || n >= 40 {
			continue
		}
		n++
		m := m
		items = append(items, menuItem{key: "m" + m.User.ID.String(), text: c.memberName(cs.ch.GuildID, m.User.ID, &m), act: func() {
			ow := discord.Overwrite{ID: discord.Snowflake(m.User.ID), Type: discord.OverwriteMember}
			c.putOverwrite(ow)
			cs.target = ow.ID
		}})
	}
	return items
}

// createWebhook makes a webhook in the channel, named as Discord names
// them.
func (c *chatView) createWebhook() {
	cs := &c.cset
	names := []string{"Captain Hook", "Spidey Bot", "Hooky", "Webhook"}
	name := names[len(cs.hooks.data)%len(names)]
	if c.demo {
		cs.hooks.data = append(cs.hooks.data, webhook{ID: strconv.Itoa(len(cs.hooks.data) + 1), Type: 1, Name: name, ChannelID: cs.ch.ID, User: &c.me})
		return
	}
	id := cs.ch.ID
	go func() {
		h, err := c.st.Client.RequestJSON[webhook]("POST", api.EndpointChannels+id.String()+"/webhooks", httputil.WithJSONBody(map[string]any{"name": name}))
		c.u.post(func() {
			if cs.ch.ID != id {
				return
			}
			if err != nil {
				cs.err = "Discord did not make the webhook: " + err.Error()
				return
			}
			cs.hooks.data = append(cs.hooks.data, h)
			if cs.hookOpen == nil {
				cs.hookOpen = map[string]bool{}
			}
			cs.hookOpen[h.ID] = true
		})
	}()
}

// pickWebhookAvatar asks for a picture for the webhook.
func (c *chatView) pickWebhookAvatar(hid string) {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		path, err := zenity.SelectFile(zenity.Title("Webhook avatar"), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"}})
		if err != nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 8<<20 {
			return
		}
		uri := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
		c.u.post(func() {
			c.channelRequest("PATCH", api.EndpointWebhooks+hid, map[string]any{"avatar": uri}, func() {
				c.cset.hooks.loaded = false
				c.reloadChannelPageLater(csIntegrations)
			})
		})
	}()
}

// reloadChannelPageLater asks for a page's list again in a moment.
func (c *chatView) reloadChannelPageLater(p int) {
	time.AfterFunc(time.Second, func() {
		c.u.post(func() {
			if c.cset.open && c.cset.page == p {
				c.showChannelPage(p)
			}
		})
	})
}

// askDeleteChannel asks whether to delete the channel, and deletes it.
func (c *chatView) askDeleteChannel(ch discord.Channel) {
	what := "Channel"
	if ch.Type == discord.GuildCategory {
		what = "Category"
	}
	c.prompt("Delete "+what, "Delete "+what, true, []string{"Are you sure you want to delete #" + ch.Name + "? This cannot be undone."}, nil, func([]string) {
		if c.demo {
			c.st.Cabinet.ChannelRemove(&ch)
			c.cset.open = false
			c.rebuildRows()
			return
		}
		go func() {
			err := c.st.Client.FastRequest("DELETE", api.EndpointChannels+ch.ID.String())
			c.u.post(func() {
				if err != nil {
					c.u.voiceFailed("Could not delete the channel", err)
					return
				}
				if c.cset.ch.ID == ch.ID {
					c.cset.open = false
				}
			})
		}()
	})
}

// layoutSettingsScreen lays out a settings screen as the user's: the
// sidebar at the left, the page in the middle, and the close button.
func layoutSettingsScreen(gtx layout.Context, th *material.Theme, sidebar, page layout.Widget, closeBtn *widget.Clickable) layout.Dimensions {
	size := gtx.Constraints.Max
	sideW := min(gtx.Dp(232)+max(size.X-gtx.Dp(232+40+740+72), 0)/2, size.X/2)
	paint.FillShape(gtx.Ops, colorSidebar, clip.Rect{Max: image.Pt(sideW, size.Y)}.Op())
	paint.FillShape(gtx.Ops, colorChat, clip.Rect{Min: image.Pt(sideW, 0), Max: size}.Op())
	listW := max(min(gtx.Dp(218), sideW-gtx.Dp(12)), 0)
	top := gtx.Dp(60)
	t := op.Offset(image.Pt(sideW-listW-gtx.Dp(6), top)).Push(gtx.Ops)
	sgtx := gtx
	sgtx.Constraints = layout.Constraints{Min: image.Pt(listW, 0), Max: image.Pt(listW, max(size.Y-top, 0))}
	sidebar(sgtx)
	t.Pop()
	x := sideW + gtx.Dp(40)
	w := max(min(gtx.Dp(740), size.X-x-gtx.Dp(72)), 0)
	t = op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
	pgtx := gtx
	pgtx.Constraints = layout.Exact(image.Pt(w, size.Y))
	page(pgtx)
	t.Pop()
	t = op.Offset(image.Pt(x+w+gtx.Dp(16), top)).Push(gtx.Ops)
	closeCircle(gtx, th, closeBtn)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// settingsPageList lays out a settings page's rows, with its title and
// what last happened, scrolled as a whole.
func settingsPageList(gtx layout.Context, th *material.Theme, list *widget.List, title, errText, info string, rows []layout.Widget) layout.Dimensions {
	head := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(60), Bottom: unit.Dp(16)}.Layout(gtx, bold(label(th, 20, colorHeader, title)).Layout)
	}}
	switch {
	case errText != "":
		head = append(head, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, errText).Layout)
		})
	case info != "":
		head = append(head, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorGreen, info).Layout)
		})
	}
	rows = append(head, rows...)
	rows = append(rows, layout.Spacer{Height: unit.Dp(80)}.Layout)
	return listLayout(gtx, th, list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, rows[i])
	})
}

func (c *chatView) layoutChannelSettings(gtx layout.Context) layout.Dimensions {
	c.updateChannelSettings(gtx)
	if !c.cset.open {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	th := c.u.th
	cs := &c.cset
	return layoutSettingsScreen(gtx, th, c.layoutChannelSidebar, func(gtx layout.Context) layout.Dimensions {
		var rows []layout.Widget
		switch cs.page {
		case csOverview:
			rows = c.channelOverviewRows()
		case csPermissions:
			rows = c.channelPermissionRows()
		case csInvites:
			rows = c.channelInviteRows()
		case csIntegrations:
			rows = c.channelIntegrationRows()
		}
		return settingsPageList(gtx, th, &cs.list, csPageNames[cs.page], cs.err, cs.info, rows)
	}, &cs.closeBtn)
}

func (c *chatView) layoutChannelSidebar(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	cs := &c.cset
	title := "# " + strings.ToUpper(cs.ch.Name)
	if cs.ch.Type == discord.GuildCategory {
		title = "▸ " + strings.ToUpper(cs.ch.Name)
	}
	if parent, err := c.st.Cabinet.Channel(cs.ch.ParentID); err == nil {
		title += "  " + strings.ToUpper(parent.Name)
	}
	children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(10), Bottom: unit.Dp(6), Top: unit.Dp(10)}.Layout(gtx, oneLine(bold(label(th, 12, colorMuted, title))).Layout)
	})}
	pages := csPages
	if cs.ch.Type == discord.GuildCategory {
		pages = csInvites
	}
	for p := range pages {
		p := p
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rowItem(gtx, &cs.pages[p], func(gtx layout.Context, hovered bool) layout.Dimensions {
					fg := colorMuted
					if hovered || cs.page == p {
						fg = colorHeader
					}
					return oneLine(label(th, 16, fg, csPageNames[p])).Layout(gtx)
				}, cs.page == p)
			})
		}))
	}
	what := "Delete Channel"
	if cs.ch.Type == discord.GuildCategory {
		what = "Delete Category"
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 8) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, &cs.del, func(gtx layout.Context, hovered bool) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(label(th, 16, colorRed, what)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconDelete, 16, colorRed) }),
				)
			}, false)
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// channelOverviewRows are the Overview page.
func (c *chatView) channelOverviewRows() []layout.Widget {
	th := c.u.th
	cs := &c.cset
	ch := &cs.ch
	topicLen := utf8.RuneCountInString(cs.topic.Text())
	rows := []layout.Widget{
		section(th, kindName(*ch)+" Name", ""),
		func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &cs.name, "channel-name") },
		layout.Spacer{Height: unit.Dp(16)}.Layout,
	}
	if !isVoiceKind(ch.Type) && ch.Type != discord.GuildCategory {
		rows = append(rows,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Flexed(1, section(th, "Channel Topic", "")),
					layout.Rigid(label(th, 12, colorMuted, fmt.Sprintf("%d", 1024-topicLen)).Layout),
				)
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Dp(100)
				return textField(gtx, th, &cs.topic, "Let everyone know how to use this channel!")
			},
			layout.Spacer{Height: unit.Dp(16)}.Layout,
		)
	}
	if ch.Type != discord.GuildCategory {
		rows = append(rows,
			section(th, "Slowmode", ""),
			func(gtx layout.Context) layout.Dimensions {
				return dropdown(gtx, th, &cs.slowBtn, slowmodeName(ch.UserRateLimit))
			},
			settingsText(th, "Members will be restricted to sending one message and creating one thread per this interval, unless they have Bypass Slowmode."),
			layout.Spacer{Height: unit.Dp(16)}.Layout,
		)
	}
	if isVoiceKind(ch.Type) {
		high := c.maxBitrate()
		// Discord makes voice channels at 64kbps.
		bitrate := min(max(cmp.Or(ch.VoiceBitrate, 64000), 8000), high)
		if cs.bitrate.dragging {
			bitrate = c.bitrateAt(cs.bitrate.value)
		}
		limit := ch.VoiceUserLimit
		if cs.limit.dragging {
			limit = uint(cs.limit.value*99 + 0.5)
		}
		limitText := "∞"
		if limit > 0 {
			limitText = fmt.Sprintf("%d users", limit)
		}
		quality := []string{"Auto", "720p"}
		rows = append(rows,
			sliderRow(th, "Bitrate", fmt.Sprintf("8kbps to %dkbps. Going over 64kbps may hurt people on poor connections.", high/1000),
				&cs.bitrate, float32(bitrate-8000)/float32(high-8000), fmt.Sprintf("%dkbps", bitrate/1000)),
			section(th, "Video Quality", "Sets camera video quality for everyone in the channel. Choose Auto for the best performance."),
		)
		for i, q := range quality {
			i, q := i, q
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				on := int(ch.VideoQualityMode) == i+1 || i == 0 && ch.VideoQualityMode == 0
				return radioRow(gtx, th, &cs.videoQ[i], q, on)
			})
		}
		rows = append(rows,
			layout.Spacer{Height: unit.Dp(12)}.Layout,
			sliderRow(th, "User Limit", "Limits the number of users that can connect to this voice channel. Members with Move Members can ignore it.",
				&cs.limit, float32(limit)/99, limitText),
			section(th, "Region Override", "Everyone in this channel connects to the region chosen, wherever they live."),
			func(gtx layout.Context) layout.Dimensions {
				return dropdown(gtx, th, &cs.regionBtn, regionName(ch.RTCRegionID))
			},
			layout.Spacer{Height: unit.Dp(16)}.Layout,
		)
	}
	if ch.Type != discord.GuildCategory {
		rows = append(rows,
			section(th, "Content Visibility", ""),
			func(gtx layout.Context) layout.Dimensions {
				return radioRow(gtx, th, &cs.visibility[0], "Default — the channel's content is always visible", !ch.NSFW)
			},
			func(gtx layout.Context) layout.Dimensions {
				return radioRow(gtx, th, &cs.visibility[1], "Age-Restricted Channel — users confirm they are of legal age to see it", ch.NSFW)
			},
			layout.Spacer{Height: unit.Dp(16)}.Layout,
		)
	}
	if ch.Type == discord.GuildText || ch.Type == discord.GuildAnnouncement || ch.Type == discord.GuildForum {
		rows = append(rows,
			section(th, "Hide After Inactivity", ""),
			func(gtx layout.Context) layout.Dimensions {
				return dropdown(gtx, th, &cs.archiveBtn, archiveName(ch.DefaultAutoArchiveDuration))
			},
			settingsText(th, "New threads will stop showing in the channel list after being inactive for the specified duration."),
		)
	}
	if strings.TrimSpace(cs.name.Text()) != cs.origName || strings.TrimSpace(cs.topic.Text()) != cs.origTopic {
		rows = append(rows, saveBar(th, true, false, "", "", &cs.reset, &cs.save))
	}
	return rows
}

// A permission of a channel, with what it lets members do.
type channelPerm struct {
	bit         uint
	name, about string
}

// channelPermGroups are the permissions a channel overrides, grouped as
// Discord's settings list them; voice says which only voice channels have.
var channelPermGroups = []struct {
	title       string
	voice, text bool
	perms       []channelPerm
}{
	{"General Channel Permissions", true, true, []channelPerm{
		{10, "View Channel", "Allows members to view this channel by default. Disabling this for @everyone will make this channel private."},
		{4, "Manage Channel", "Allows members to change this channel's name, description and text settings. They can also delete the channel."},
		{28, "Manage Permissions", "Allows members to change this channel's permissions."},
		{29, "Manage Webhooks", "Allows members to create, edit or delete webhooks in this channel, which can post messages from other apps or sites."},
	}},
	{"Membership Permissions", true, true, []channelPerm{
		{0, "Create Invite", "Allows members to invite new people to this server via a direct invite link to this channel."},
	}},
	{"Text Channel Permissions", true, true, []channelPerm{
		{11, "Send Messages and Create Posts", "Allows members to send messages in this channel and create posts in forum channels."},
		{38, "Send Messages in Threads and Posts", "Allows members to send messages in threads and in posts on forum channels."},
		{35, "Create Public Threads", "Allows members to create threads that everyone in a channel can view."},
		{36, "Create Private Threads", "Allows members to create invite-only threads."},
		{14, "Embed Links", "Allows links that members share to show embedded content in text channels."},
		{15, "Attach Files", "Allows members to upload files or media in text channels."},
		{6, "Add Reactions", "Allows members to add new emoji reactions to a message."},
		{18, "Use External Emoji", "Allows members to use emoji from other servers, if they're a Discord Nitro member."},
		{37, "Use External Stickers", "Allows members to use stickers from other servers, if they're a Discord Nitro member."},
		{17, "Mention @everyone, @here and All Roles", "Allows members to use @everyone or @here to ping all members, and to @mention every role."},
		{13, "Manage Messages", "Allows members to delete messages by other members or pin any message."},
		{51, "Pin Messages", "Allows members to pin or unpin any message."},
		{52, "Bypass Slowmode", "Allows members to send messages without being affected by slowmode."},
		{34, "Manage Threads and Posts", "Allows members to rename, delete, close and turn on slow mode for threads and posts."},
		{16, "Read Message History", "Allows members to read previous messages sent in channels."},
		{12, "Send Text-to-Speech Messages", "Allows members to send text-to-speech messages by starting a message with /tts."},
		{46, "Send Voice Messages", "Allows members to send voice messages."},
		{49, "Create Polls", "Allows members to create polls."},
	}},
	{"Voice Channel Permissions", true, false, []channelPerm{
		{20, "Connect", "Allows members to join this voice channel and hear others."},
		{21, "Speak", "Allows members to talk in this voice channel."},
		{9, "Video", "Allows members to share their video, screen share, or stream a game in this channel."},
		{42, "Use Soundboard", "Allows members to send sounds from the server soundboard."},
		{45, "Use External Sounds", "Allows members to use sounds from other servers, if they're a Discord Nitro member."},
		{25, "Use Voice Activity", "Allows members to speak in this voice channel by simply talking. If disabled, they must use Push-to-Talk."},
		{8, "Priority Speaker", "Allows members to be more easily heard in this voice channel."},
		{22, "Mute Members", "Allows members to mute other members in this voice channel for everyone."},
		{23, "Deafen Members", "Allows members to deafen other members in this voice channel."},
		{24, "Move Members", "Allows members to disconnect or move other members between voice channels they have access to."},
		{48, "Set Voice Channel Status", "Allows members to create and edit voice channel status."},
	}},
	{"Apps Permissions", true, true, []channelPerm{
		{31, "Use Application Commands", "Allows members to use commands from applications, including slash commands and context menu commands."},
		{39, "Use Activities", "Allows members to use Activities in this channel."},
		{50, "Use External Apps", "Allows apps that members have added to their account to post messages in this channel."},
	}},
	{"Events Permissions", true, true, []channelPerm{
		{44, "Create Events", "Allows members to create events."},
		{33, "Manage Events", "Allows members to edit and cancel events."},
	}},
}

// synced reports whether the channel has its category's permissions.
func (c *chatView) synced(ch discord.Channel) (parent *discord.Channel, ok bool) {
	parent, err := c.st.Cabinet.Channel(ch.ParentID)
	if err != nil {
		return nil, true
	}
	if len(parent.Overwrites) != len(ch.Overwrites) {
		return parent, false
	}
	for _, p := range parent.Overwrites {
		i := slices.IndexFunc(ch.Overwrites, func(o discord.Overwrite) bool { return o.ID == p.ID })
		if i < 0 || ch.Overwrites[i].Allow != p.Allow || ch.Overwrites[i].Deny != p.Deny {
			return parent, false
		}
	}
	return parent, true
}

// overwriteName is the role or member an overwrite is of.
func (c *chatView) overwriteName(o discord.Overwrite) (string, colorT) {
	cs := &c.cset
	if o.ID == discord.Snowflake(cs.ch.GuildID) {
		return "@everyone", colorText
	}
	if o.Type == discord.OverwriteRole {
		if r, err := c.st.Cabinet.Role(cs.ch.GuildID, discord.RoleID(o.ID)); err == nil {
			col := colorText
			if r.Color != 0 {
				col = rgb(uint32(r.Color))
			}
			return r.Name, col
		}
		return "Unknown role", colorMuted
	}
	return c.memberName(cs.ch.GuildID, discord.UserID(o.ID), nil), colorText
}

// channelPermissionRows are the Permissions page.
func (c *chatView) channelPermissionRows() []layout.Widget {
	th := c.u.th
	cs := &c.cset
	ch := &cs.ch
	everyone := c.overwrite(discord.Snowflake(ch.GuildID))
	private := everyone.Deny.Has(discord.PermissionViewChannel)
	rows := []layout.Widget{settingsText(th, "Use permissions to customise who can do what in this channel."), layout.Spacer{Height: unit.Dp(12)}.Layout}
	if parent, ok := c.synced(*ch); parent != nil {
		text := "Permissions synced with category: " + parent.Name
		if !ok {
			text = "Permissions are not synced with category: " + parent.Name
		}
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 14, colorText, text).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if ok {
						return layout.Dimensions{}
					}
					return button(gtx, th, &cs.syncBtn, colorSecondaryButton, "Sync Now")
				}),
			)
		}))
	}
	rows = append(rows,
		settingsCard(func(gtx layout.Context) layout.Dimensions {
			if ch.Type == discord.GuildCategory {
				return switchRow(gtx, th, &cs.privateBtn, "🔒 Private Category", "By making a category private, only select members and roles will be able to view it. Linked channels in it will match its settings.", private)
			}
			return switchRow(gtx, th, &cs.privateBtn, "🔒 Private Channel", "By making a channel private, only select members and roles will be able to view this channel.", private)
		}),
		gap(16),
		section(th, "Advanced permissions", ""),
	)
	// The roles and members with their own permissions here, @everyone
	// first, beside the permissions of the one chosen.
	targets := []discord.Overwrite{everyone}
	for _, o := range ch.Overwrites {
		if o.ID != everyone.ID {
			targets = append(targets, o)
		}
	}
	if cs.targets == nil {
		cs.targets = map[discord.Snowflake]*widget.Clickable{}
	}
	if !slices.ContainsFunc(targets, func(o discord.Overwrite) bool { return o.ID == cs.target }) {
		cs.target = everyone.ID
	}
	side := func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, bold(label(th, 12, colorMuted, "ROLES/MEMBERS")).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, &cs.addTarget, iconMenuChannel, colorMuted, colorHeader)
				}),
			)
		})}
		for _, o := range targets {
			o := o
			name, col := c.overwriteName(o)
			click := clickFor(&cs.targets, o.ID)
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
						return oneLine(label(th, 14, col, name)).Layout(gtx)
					}, cs.target == o.ID)
				})
			}))
		}
		if cs.target != everyone.ID {
			children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, &cs.dropTarget, colorSecondaryButton, "Remove")
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
	ow := c.overwrite(cs.target)
	if cs.perms == nil {
		cs.perms = map[uint]*[3]widget.Clickable{}
	}
	// The permissions are searched by name and what they do.
	cs.permSearch.SingleLine = true
	q := strings.ToLower(strings.TrimSpace(cs.permSearch.Text()))
	permRows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return textField(gtx, th, &cs.permSearch, "Search permissions")
	})}
	voice := isVoiceKind(ch.Type) || ch.Type == discord.GuildCategory
	found := false
	for _, g := range channelPermGroups {
		if g.voice && !g.text && !voice {
			continue
		}
		var shown []channelPerm
		for _, p := range g.perms {
			if q == "" || strings.Contains(strings.ToLower(p.name+" "+p.about), q) {
				shown = append(shown, p)
			}
		}
		if len(shown) == 0 {
			continue
		}
		found = true
		g := g
		permRows = append(permRows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(6)}.Layout(gtx, bold(label(th, 18, colorHeader, g.title)).Layout)
		}))
		for _, p := range shown {
			p := p
			clicks := c.cset.perms[p.bit]
			if clicks == nil {
				clicks = new([3]widget.Clickable)
				cs.perms[p.bit] = clicks
			}
			bit := discord.Permissions(1) << p.bit
			state := 1
			switch {
			case ow.Deny&bit != 0:
				state = 0
			case ow.Allow&bit != 0:
				state = 2
			}
			permRows = append(permRows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Flexed(1, stacked(bold(label(th, 15, colorHeader, p.name)).Layout, label(th, 13, colorMuted, p.about).Layout)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return triState(gtx, th, clicks, state) }),
					)
				})
			}), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 0) }))
		}
	}
	if !found {
		permRows = append(permRows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, label(th, 14, colorMuted, "No permissions match.").Layout)
		}))
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(200)
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return side(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(20)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, permRows...)
			}),
		)
	})
	return rows
}

// triState is Discord's switch of deny, inherit and allow.
func triState(gtx layout.Context, th *material.Theme, clicks *[3]widget.Clickable, state int) layout.Dimensions {
	marks := [3]struct {
		text string
		col  colorT
		bg   colorT
	}{{"✕", colorRed, colorRed}, {"/", colorText, colorSelected}, {"✓", colorGreen, colorGreen}}
	return rounded(gtx, colorServers, 6, func(gtx layout.Context) layout.Dimensions {
		var cells []layout.FlexChild
		for i := range clicks {
			i := i
			cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return clicks[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					size := image.Pt(gtx.Dp(32), gtx.Dp(28))
					fg := marks[i].col
					if state == i {
						bg := marks[i].bg
						if i != 1 {
							bg = withAlpha(bg, 0xff)
							fg = colorHeader
						}
						paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Op(gtx.Ops))
					}
					gtx.Constraints = layout.Exact(size)
					return layout.Center.Layout(gtx, bold(label(th, 15, fg, marks[i].text)).Layout)
				})
			}))
		}
		return layout.Flex{}.Layout(gtx, cells...)
	})
}

// channelInviteRows are the Invites page, of this channel's invites only.
func (c *chatView) channelInviteRows() []layout.Widget {
	th := c.u.th
	cs := &c.cset
	l := &c.gset.lists
	pause := "Pause Invites"
	if l.invitesPaused() {
		pause = "Resume Invites"
	}
	rows := []layout.Widget{
		settingsText(th, "Here is a list of all active invite links. You can revoke any one or create one."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return button(gtx, th, &cs.pause, colorRed, pause) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &cs.createInvite, colorAccent, "Create Invite")
				}),
			)
		},
		gap(12),
	}
	rows = append(rows, cs.invites.note(th)...)
	if !cs.invites.loaded {
		return rows
	}
	if len(cs.invites.data) == 0 {
		return append(rows, settingsText(th, "No invites to this channel yet."))
	}
	widths := []float32{2.4, 1.6, 0.8, 1.4, 0.4}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		h := func(t string) layout.Widget { return bold(label(th, 12, colorMuted, t)).Layout }
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
		return tableRow(gtx, widths, h("INVITER"), h("INVITE CODE"), h("USES"), h("EXPIRES"), h(""))
	})
	for _, inv := range cs.invites.data {
		inv := inv
		click := clickFor(&cs.revoke, inv.Code)
		rows = append(rows, c.inviteRow(inv, widths, click))
	}
	return rows
}

// channelIntegrationRows are the Integrations page: the channel's webhooks
// and the channels it follows.
func (c *chatView) channelIntegrationRows() []layout.Widget {
	th := c.u.th
	cs := &c.cset
	rows := []layout.Widget{
		settingsText(th, "Customize your server with integrations. Manage webhooks and followed channels posting in this channel."),
		gap(12),
	}
	rows = append(rows, cs.hooks.note(th)...)
	if !cs.hooks.loaded {
		return rows
	}
	var hooks []webhook
	followed := 0
	for _, h := range cs.hooks.data {
		if h.Type == 2 {
			followed++
			continue
		}
		hooks = append(hooks, h)
	}
	rows = append(rows,
		settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, stacked(bold(label(th, 16, colorHeader, "Webhooks")).Layout, label(th, 13, colorMuted, fmt.Sprintf("%d webhook%s", len(hooks), plural(len(hooks)))).Layout)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &cs.newHook, colorAccent, "New Webhook")
				}),
			)
		}),
		settingsCard(stacked(bold(label(th, 16, colorHeader, "Channels Followed")).Layout, label(th, 13, colorMuted, fmt.Sprintf("%d channel%s", followed, plural(followed))).Layout)),
	)
	if len(hooks) > 0 {
		rows = append(rows, layout.Spacer{Height: unit.Dp(12)}.Layout, section(th, "Posting in #"+cs.ch.Name, ""))
	}
	if cs.hookOpen == nil {
		cs.hookOpen = map[string]bool{}
	}
	if cs.hookName == nil {
		cs.hookName = map[string]*widget.Editor{}
	}
	for _, h := range hooks {
		h := h
		toggle := clickFor(&cs.hookToggle, h.ID)
		ed := cs.hookName[h.ID]
		if ed == nil {
			ed = &widget.Editor{SingleLine: true}
			ed.SetText(h.Name)
			cs.hookName[h.ID] = ed
		}
		avatar := "https://cdn.discordapp.com/embed/avatars/0.png"
		if h.Avatar != "" {
			avatar = "https://cdn.discordapp.com/avatars/" + h.ID + "/" + h.Avatar + ".png?size=128"
		}
		by := ""
		if h.User != nil {
			by = " by " + h.User.Username
		}
		created := ""
		if id, err := discord.ParseSnowflake(h.ID); err == nil {
			created = "Created on " + id.Time().Format("2 Jan 2006") + by
		}
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			head := func(gtx layout.Context) layout.Dimensions {
				return toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return cardRow(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, avatar, 40, colorAccent, h.Name, -1)
					}, h.Name, created, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconExpand, 20, colorMuted) })
				})
			}
			if !cs.hookOpen[h.ID] {
				return head(gtx)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(head),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 10) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return clickFor(&cs.hookAvatar, h.ID).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								return stacked(
									func(gtx layout.Context) layout.Dimensions {
										return c.u.picture(gtx, avatar, 88, colorAccent, h.Name, gtx.Dp(12))
									},
									label(th, 11, colorMuted, "Change avatar").Layout,
								)(gtx)
							})
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
						layout.Flexed(1, stacked(
							func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{}.Layout(gtx,
									layout.Flexed(1, stacked(settingsHeading(th, "NAME"), func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, ed, "Webhook name") })),
									layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
									layout.Flexed(1, stacked(settingsHeading(th, "CHANNEL"), func(gtx layout.Context) layout.Dimensions {
										return dropdown(gtx, th, clickFor(&cs.hookChan, h.ID), "#"+c.channelName(h.ChannelID, cs.ch.Name))
									})),
								)
							},
							func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 10) },
							func(gtx layout.Context) layout.Dimensions {
								children := []layout.FlexChild{
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return button(gtx, th, clickFor(&cs.hookCopy, h.ID), colorSecondaryButton, "Copy Webhook URL")
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return button(gtx, th, clickFor(&cs.hookDelete, h.ID), colorRed, "Delete Webhook")
									}),
								}
								if strings.TrimSpace(ed.Text()) != h.Name {
									children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return button(gtx, th, clickFor(&cs.hookSave, h.ID), colorGreenButton, "Save")
									}))
								}
								return layout.Flex{}.Layout(gtx, children...)
							},
						)),
					)
				}),
			)
		}))
	}
	return rows
}
