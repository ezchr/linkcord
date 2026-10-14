package gui

import (
	"cmp"
	"image"
	"slices"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/ningen/v3"
)

// maxUnreadDMs is how many unread conversations the server column shows.
const maxUnreadDMs = 8

// dmUnreadCount is how many unread messages a conversation has. Every
// message in one counts as a mention, which Discord keeps the number of.
func (c *chatView) dmUnreadCount(chID discord.ChannelID) int {
	rs := c.st.ReadState.ReadState(chID)
	if rs == nil {
		return 0
	}
	if rs.MentionCount > 0 {
		return rs.MentionCount
	}
	// Otherwise the loaded messages after the last one read, at least one.
	n := 0
	if msgs, _ := c.st.Cabinet.Messages(chID); msgs != nil {
		for _, m := range msgs {
			if m.ID <= rs.LastMessageID {
				break
			}
			n++
		}
	}
	if n == 0 && c.channelUnread(chID) != ningen.ChannelRead {
		n = 1
	}
	return n
}

// unreadDMs are the conversations with unread messages, newest first, as
// Discord shows them above the servers.
func (c *chatView) unreadDMs() []discord.Channel {
	var out []discord.Channel
	for _, ch := range c.privateChannels() {
		if c.friends.requestIDs[ch.ID] {
			continue
		}
		if c.channelUnread(ch.ID) != ningen.ChannelRead {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b discord.Channel) int { return cmp.Compare(b.LastMessageID, a.LastMessageID) })
	if len(out) > maxUnreadDMs {
		out = out[:maxUnreadDMs]
	}
	return out
}

// layoutUnreadDM is an unread conversation in the server column: its
// picture with the number of unread messages; a click opens it.
func (c *chatView) layoutUnreadDM(gtx layout.Context, ch discord.Channel) layout.Dimensions {
	// Its own click, apart from the conversation's row in the list.
	cl := clickOf(c.unreadDMClicks, ch.ID)
	if cl.Clicked(gtx) {
		c.selectGuild(discord.NullGuildID)
		c.selectChannel(ch)
	}
	selected := c.selChannel != nil && c.selChannel.ID == ch.ID
	name := c.dmName(ch)
	dims := c.guildButton(gtx, cl, selected, ningen.ChannelRead, func(gtx layout.Context, active bool) layout.Dimensions {
		if ch.Type == discord.DirectMessage && len(ch.DMRecipients) > 0 {
			u := ch.DMRecipients[0]
			return c.u.picture(gtx, c.authorAvatarURL(discord.NullGuildID, u), 48, colorFor(uint64(u.ID)), name, -1)
		}
		url := ""
		if ch.Icon != "" {
			url = cdnSize(ch.IconURLWithType(discord.PNGImage))
		}
		return c.u.picture(gtx, url, 48, colorFor(uint64(ch.ID)), name, -1)
	})
	// The count sits on the bottom right of the picture, as on Discord.
	if n := c.dmUnreadCount(ch.ID); n > 0 {
		macro := op.Record(gtx.Ops)
		bd := outlinedBadge(gtx, c, n)
		call := macro.Stop()
		x := dims.Size.X/2 + gtx.Dp(28) - bd.Size.X
		y := dims.Size.Y - bd.Size.Y + gtx.Dp(2)
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
	return dims
}

// outlinedBadge is the count badge with a ring in the column's color, which
// sets it apart from the picture under it.
func outlinedBadge(gtx layout.Context, c *chatView, n int) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	return rounded(gtx, colorServers, 11, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(3)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return countBadge(gtx, c.u, n)
		})
	})
}
