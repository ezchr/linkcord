package gui

import (
	"gioui.org/layout"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
)

// roleLook is what a member's roles give their name: the color of the highest
// role with one, the color the name fades to after it if that role has a
// gradient, and the emoji of the highest role with one.
type roleLook struct {
	color, second discord.Color
	emoji         string
}

// named is the name with the emoji of the role before it, as Discord shows
// it.
func (l roleLook) named(name string) string {
	if l.emoji == "" {
		return name
	}
	return l.emoji + " " + name
}

// colored reports whether a role gives the name its color.
func (l roleLook) colored() bool { return l.color > 0 }

// gradient reports whether the name fades from the role's color to another.
func (l roleLook) gradient() bool { return l.colored() && l.second > 0 }

// roleChipName is the name of a role's chip on a profile: with its emoji, and
// in its gradient if it has one.
func roleChipName(th *material.Theme, role discord.Role) layout.Widget {
	text := role.Name
	if role.UnicodeEmoji != "" {
		text = role.UnicodeEmoji + " " + text
	}
	l := label(th, 12, colorText, text)
	if role.Color <= 0 || role.Colors.Secondary <= 0 {
		return l.Layout
	}
	return func(gtx layout.Context) layout.Dimensions {
		return gradientName(gtx, l, rgb(uint32(role.Color)), rgb(uint32(role.Colors.Secondary)))
	}
}

// memberRoleLook is what the roles of a member give their name, from the
// cache. The colors are left out when they are turned off, the emoji not.
func (c *chatView) memberRoleLook(guildID discord.GuildID, userID discord.UserID) roleLook {
	var look roleLook
	if !guildID.IsValid() {
		return look
	}
	m, err := c.st.Cabinet.Member(guildID, userID)
	if err != nil {
		return look
	}
	colorPos, emojiPos := -1, -1
	for _, id := range m.RoleIDs {
		r, err := c.st.Cabinet.Role(guildID, id)
		if err != nil {
			continue
		}
		if r.Color > 0 && r.Position > colorPos && !c.u.app.NoRoleColors {
			look.color, look.second, colorPos = r.Color, r.Colors.Secondary, r.Position
		}
		if r.UnicodeEmoji != "" && r.Position > emojiPos {
			look.emoji, emojiPos = r.UnicodeEmoji, r.Position
		}
	}
	return look
}
