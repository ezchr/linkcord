package gui

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state"
)

// The window's thread must never wait for Discord. State's own lookups fetch
// what the cache lacks over HTTP and wait for the answer, one request per
// call: drawing a busy server that way froze the window for seconds each
// frame. These read the cache only, and ask the gateway in the background
// for what is missing; its answer redraws the window.

// memberColor is the color of a member's name, from the cache.
func (c *chatView) memberColor(guildID discord.GuildID, userID discord.UserID) (discord.Color, bool) {
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
// cache; ok is false when something they depend on has not loaded yet.
func (c *chatView) cachedPerms(chID discord.ChannelID) (discord.Permissions, bool) {
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
