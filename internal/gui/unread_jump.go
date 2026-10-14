package gui

import (
	"gioui.org/io/key"
	"gioui.org/layout"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/ningen/v3"
)

// updateUnreadKeys jumps between unread places with Discord's keys:
// Alt+Shift+Up/Down to the previous or next unread channel, Ctrl+Alt+Up/Down
// to the previous or next unread server.
func (c *chatView) updateUnreadKeys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: key.NameUpArrow, Required: key.ModAlt | key.ModShift},
			key.Filter{Name: key.NameDownArrow, Required: key.ModAlt | key.ModShift},
			key.Filter{Name: key.NameUpArrow, Required: key.ModAlt | key.ModCtrl},
			key.Filter{Name: key.NameDownArrow, Required: key.ModAlt | key.ModCtrl},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		step := 1
		if e.Name == key.NameUpArrow {
			step = -1
		}
		if e.Modifiers.Contain(key.ModCtrl) {
			c.jumpUnreadGuild(step)
		} else {
			c.jumpUnreadChannel(step)
		}
	}
}

// jumpUnreadChannel opens the next unread channel of the list, in the
// direction of step, going round.
func (c *chatView) jumpUnreadChannel(step int) {
	cur := -1
	for i, r := range c.rows {
		if c.selChannel != nil && (r.kind == rowText || r.kind == rowDM) && r.ch.ID == c.selChannel.ID {
			cur = i
		}
	}
	n := len(c.rows)
	for k := 1; k <= n; k++ {
		i := ((cur+step*k)%n + n) % n
		r := c.rows[i]
		if (r.kind == rowText || r.kind == rowDM) && c.channelUnread(r.ch.ID) != ningen.ChannelRead {
			c.selectChannel(r.ch)
			return
		}
	}
}

// jumpUnreadGuild opens the next unread server, or direct messages, in the
// direction of step, going round.
func (c *chatView) jumpUnreadGuild(step int) {
	// Direct messages come first, as in the list of servers.
	places := append([]discord.GuildID{0}, c.guildOrder...)
	cur := 0
	for i, id := range places {
		if id == c.selGuild {
			cur = i
		}
	}
	n := len(places)
	for k := 1; k < n; k++ {
		id := places[((cur+step*k)%n+n)%n]
		unread := false
		if id.IsValid() {
			unread = c.guildUnread(id) != ningen.ChannelRead
		} else {
			unread = c.dmsUnread() != ningen.ChannelRead
		}
		if unread {
			c.selectGuild(id)
			c.jumpUnreadChannel(1)
			return
		}
	}
}
