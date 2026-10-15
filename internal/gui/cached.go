package gui

import (
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/ningen/v3"
)

// The window's thread must never wait for Discord. State's own lookups fetch
// what the cache lacks over HTTP and wait for the answer, one request per
// call: drawing a busy server that way froze the window for seconds each
// frame. These read the cache only, and ask the gateway in the background
// for what is missing; its answer redraws the window.

// memberColor is the color of a member's name, from the cache.
func (c *chatView) memberColor(guildID discord.GuildID, userID discord.UserID) (discord.Color, bool) {
	if c.u.app.NoRoleColors {
		return discord.NullColor, false
	}
	m, err := c.st.Cabinet.Member(guildID, userID)
	if err != nil {
		return discord.NullColor, false
	}
	return state.MemberColor(m, func(id discord.RoleID) *discord.Role {
		r, _ := c.st.Cabinet.Role(guildID, id)
		return r
	})
}

// can reports whether the user has perms in a channel, from the cache. What
// is not known yet counts as allowed, so that nothing hides while it loads;
// Discord still refuses what is not.
func (c *chatView) can(chID discord.ChannelID, perms discord.Permissions) bool {
	p, ok := c.cachedPerms(chID)
	return !ok || p.Has(perms)
}

// cachedPerms are the user's permissions in a server channel, from the
// cache; ok is false when something they depend on has not loaded yet. They
// are worked out at most every second: each channel of the list asked each
// frame, copying the channel, the server and its roles.
func (c *chatView) cachedPerms(chID discord.ChannelID) (discord.Permissions, bool) {
	if c.perms == nil {
		c.perms = make(map[discord.ChannelID]cachedPerm)
	}
	if p, ok := c.perms[chID]; ok && time.Since(p.at) < time.Second {
		return p.perms, p.ok
	}
	perms, ok := c.permsNow(chID)
	c.perms[chID] = cachedPerm{at: time.Now(), perms: perms, ok: ok}
	return perms, ok
}

// cachedPerm are permissions worked out at.
type cachedPerm struct {
	at    time.Time
	perms discord.Permissions
	ok    bool
}

func (c *chatView) permsNow(chID discord.ChannelID) (discord.Permissions, bool) {
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil || !ch.GuildID.IsValid() {
		return 0, false
	}
	g, err := c.st.Cabinet.Guild(ch.GuildID)
	if err != nil {
		return 0, false
	}
	me := c.me.ID
	if !me.IsValid() {
		if u, err := c.st.Cabinet.Me(); err == nil {
			me = u.ID
		}
	}
	m, err := c.st.Cabinet.Member(ch.GuildID, me)
	if err != nil {
		if me.IsValid() {
			c.st.MemberState.RequestMember(ch.GuildID, me)
		}
		return 0, false
	}
	roles, err := c.st.Cabinet.Roles(ch.GuildID)
	if err != nil {
		return 0, false
	}
	return discord.CalcOverrides(*g, *ch, *m, roles), true
}

// privateChannels are the direct messages, from the cache.
func (c *chatView) privateChannels() []discord.Channel {
	chs, _ := c.st.Cabinet.PrivateChannels()
	return chs
}

// channelUnread is ningen's ChannelIsUnread from the cache: ningen's checks
// the permission to see the channel with State, which fetches what it lacks
// and waits, for each channel of each server, each frame.
func (c *chatView) channelUnread(chID discord.ChannelID) ningen.UnreadIndication {
	rs := c.st.ReadState.ReadState(chID)
	if rs == nil || !rs.LastMessageID.IsValid() {
		return ningen.ChannelRead
	}
	// Mentions override mutes.
	if rs.MentionCount > 0 {
		return ningen.ChannelMentioned
	}
	if c.st.ChannelIsMuted(chID, ningen.UnreadOpts{}) {
		return ningen.ChannelRead
	}
	last := c.st.LastMessage(chID)
	if !last.IsValid() || rs.LastMessageID >= last {
		return ningen.ChannelRead
	}
	if !c.can(chID, discord.PermissionViewChannel) {
		return ningen.ChannelRead
	}
	return ningen.ChannelUnread
}

// guildBadge is what a server's icon shows of its channels, worked out at.
type guildBadge struct {
	at       time.Time
	unread   ningen.UnreadIndication
	mentions int
}

// guildBadgeFor is the unread mark and mentions of a server, worked out
// again when something happens in it (see apply), or after 5 seconds: each
// frame did it for every server, copying the channels of each, which in busy
// servers redrawing often was most of the work of Linkcord.
func (c *chatView) guildBadgeFor(guildID discord.GuildID) guildBadge {
	if c.guildBadges == nil {
		c.guildBadges = make(map[discord.GuildID]guildBadge)
	}
	if b, ok := c.guildBadges[guildID]; ok && time.Since(b.at) < 5*time.Second {
		return b
	}
	b := guildBadge{at: time.Now(), unread: c.guildUnreadNow(guildID), mentions: c.guildMentionsNow(guildID)}
	c.guildBadges[guildID] = b
	return b
}

// guildUnread is ningen's GuildIsUnread from the cache.
func (c *chatView) guildUnread(guildID discord.GuildID) ningen.UnreadIndication {
	return c.guildBadgeFor(guildID).unread
}

// guildMentions is how many mentions wait in a server's channels.
func (c *chatView) guildMentions(guildID discord.GuildID) int {
	return c.guildBadgeFor(guildID).mentions
}

func (c *chatView) guildUnreadNow(guildID discord.GuildID) ningen.UnreadIndication {
	chs, err := c.st.Cabinet.Channels(guildID)
	if err != nil {
		return ningen.ChannelRead
	}
	ind := ningen.ChannelRead
	for _, ch := range chs {
		s := c.channelUnread(ch.ID)
		if s == ningen.ChannelMentioned {
			return s
		}
		ind = max(ind, s)
	}
	// Muted servers show only mentions.
	if c.st.MutedState.Guild(guildID, false) && ind != ningen.ChannelMentioned {
		return ningen.ChannelRead
	}
	return ind
}

func (c *chatView) guildMentionsNow(guildID discord.GuildID) int {
	chs, err := c.st.Cabinet.Channels(guildID)
	if err != nil {
		return 0
	}
	n := 0
	for _, ch := range chs {
		if rs := c.st.ReadState.ReadState(ch.ID); rs != nil && rs.MentionCount > 0 && c.can(ch.ID, discord.PermissionViewChannel) {
			n += rs.MentionCount
		}
	}
	return n
}
