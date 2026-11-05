package gui

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
)

// roleLook is what a member's roles give their name: the color of the highest
// role with one, the color the name fades to after it if that role has a
// gradient, and the icon of the highest role with one: an uploaded picture
// (iconRole, icon) or an emoji.
type roleLook struct {
	color, second discord.Color
	iconRole      discord.RoleID
	icon          discord.Hash
	emoji         string
}

// hasIcon reports whether a role gives the name an icon.
func (l roleLook) hasIcon() bool { return l.icon != "" || l.emoji != "" }

// withoutIcon is the look without the role icon, for where Discord shows none.
func (l roleLook) withoutIcon() roleLook {
	l.iconRole, l.icon, l.emoji = 0, "", ""
	return l
}

// roleIconURL is the uploaded icon of a role on Discord's CDN.
func roleIconURL(id discord.RoleID, icon discord.Hash) string {
	return "https://cdn.discordapp.com/role-icons/" + id.String() + "/" + string(icon) + ".png?size=64"
}

// roleIcon draws a role's icon size wide, as Discord shows it after a name: the
// uploaded picture if the role has one, else its emoji. Nothing without either.
func (c *chatView) roleIcon(gtx layout.Context, look roleLook, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	switch {
	case look.icon != "":
		gtx.Constraints = layout.Exact(image.Pt(px, px))
		if img := c.u.images.frame(gtx, roleIconURL(look.iconRole, look.icon), px); img != nil {
			widget.Image{Src: *img, Fit: widget.Contain}.Layout(gtx)
		}
		// The room is kept while it loads, so the line doesn't jump.
		return layout.Dimensions{Size: image.Pt(px, px), Baseline: px / 5}
	case look.emoji != "" && isEmoji(look.emoji):
		dims := c.twemoji(gtx, twemojiURL(look.emoji, px), size)
		dims.Baseline = px / 5
		return dims
	case look.emoji != "":
		return label(c.u.th, unit.Sp(float32(size)*0.8), colorText, look.emoji).Layout(gtx)
	}
	return layout.Dimensions{}
}

// colored reports whether a role gives the name its color.
func (l roleLook) colored() bool { return l.color > 0 }

// gradient reports whether the name fades from the role's color to another.
func (l roleLook) gradient() bool { return l.colored() && l.second > 0 }

// roleChipName is the name of a role's chip on a profile: after its icon, and
// in its gradient if it has one.
func (c *chatView) roleChipName(th *material.Theme, role discord.Role) layout.Widget {
	l := label(th, 12, colorText, role.Name)
	name := l.Layout
	if role.Color > 0 && role.Colors.Secondary > 0 {
		name = func(gtx layout.Context) layout.Dimensions {
			return gradientName(gtx, l, rgb(uint32(role.Color)), rgb(uint32(role.Colors.Secondary)))
		}
	}
	look := roleLook{iconRole: role.ID, icon: role.Icon, emoji: role.UnicodeEmoji}
	if !look.hasIcon() {
		return name
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.roleIcon(gtx, look, 14) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(name),
		)
	}
}

// memberRoleLook is what the roles of a member give their name, from the
// cache. The colors are left out when they are turned off, the icon not.
func (c *chatView) memberRoleLook(guildID discord.GuildID, userID discord.UserID) roleLook {
	var look roleLook
	if !guildID.IsValid() {
		return look
	}
	m, err := c.st.Cabinet.Member(guildID, userID)
	if err != nil {
		return look
	}
	colorPos, iconPos := -1, -1
	for _, id := range m.RoleIDs {
		r, err := c.st.Cabinet.Role(guildID, id)
		if err != nil {
			continue
		}
		if r.Color > 0 && r.Position > colorPos && !c.u.app.NoRoleColors {
			look.color, look.second, colorPos = r.Color, r.Colors.Secondary, r.Position
		}
		// The icon of the highest role with one; a role's uploaded picture
		// wins over its emoji.
		if (r.Icon != "" || r.UnicodeEmoji != "") && r.Position > iconPos {
			look.iconRole, look.icon, look.emoji, iconPos = r.ID, r.Icon, r.UnicodeEmoji, r.Position
		}
	}
	return look
}
