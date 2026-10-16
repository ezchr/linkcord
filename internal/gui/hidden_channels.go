package gui

import (
	"slices"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// Channels the user may not see are listed too, if they choose, dimmed:
// Discord sends every channel of a server, those hidden included, with
// their name, topic and who may see them, but not their messages.

// hiddenChannel reports whether ch is a server channel the user may not
// see.
func (c *chatView) hiddenChannel(ch discord.Channel) bool {
	return ch.GuildID.IsValid() && ch.Type != discord.GuildCategory && !c.can(ch.ID, discord.PermissionViewChannel)
}

// selectedHidden reports whether the channel open is one the user may not
// see, which shows what is known of it in place of its messages.
func (c *chatView) selectedHidden() bool {
	return c.selChannel != nil && c.hiddenChannel(*c.selChannel)
}

// whoSees are the roles and members a hidden channel lets see it, by name.
func (c *chatView) whoSees(ch discord.Channel) []string {
	var out []string
	for _, ow := range ch.Overwrites {
		if ow.Allow&discord.PermissionViewChannel == 0 {
			continue
		}
		switch ow.Type {
		case discord.OverwriteRole:
			if discord.RoleID(ow.ID) == discord.RoleID(ch.GuildID) {
				out = append(out, "@everyone")
			} else if r, err := c.st.Cabinet.Role(ch.GuildID, discord.RoleID(ow.ID)); err == nil {
				out = append(out, "@"+r.Name)
			}
		case discord.OverwriteMember:
			out = append(out, c.memberName(ch.GuildID, discord.UserID(ow.ID), nil))
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return slices.Compact(out)
}

// layoutHiddenChannel is what is known of a channel the user may not see.
func (c *chatView) layoutHiddenChannel(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	ch := *c.selChannel
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconLock, 48, colorMuted) },
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		bold(label(th, 22, colorHeader, "#"+ch.Name)).Layout,
		layout.Spacer{Height: unit.Dp(4)}.Layout,
		label(th, 15, colorMuted, "You do not have access to this channel. Only its name and details show.").Layout,
	}
	line := func(title, value string) {
		rows = append(rows,
			layout.Spacer{Height: unit.Dp(14)}.Layout,
			bold(label(th, 12, colorText, title)).Layout,
			label(th, 14, colorText, value).Layout,
		)
	}
	if ch.Topic != "" {
		line("TOPIC", ch.Topic)
	}
	if ch.LastMessageID.IsValid() {
		line("LAST MESSAGE", formatTime(ch.LastMessageID.Time()))
	}
	line("CREATED", formatDate(ch.CreatedAt()))
	if who := c.whoSees(ch); len(who) > 0 {
		line("WHO CAN SEE IT", strings.Join(who, ", "))
	}
	if ch.Type == discord.GuildVoice || ch.Type == discord.GuildStageVoice {
		line("KIND", "Voice channel")
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
		return layout.UniformInset(unit.Dp(24)).Layout(gtx, stacked(rows...))
	})
}
