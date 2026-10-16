package gui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Beside names: where someone is connected from, as Vencord's
// PlatformIndicators shows, and a crown for the server's owner, admins and
// those who manage it, as BetterDiscord's StaffTag does.

var (
	iconDesktop = mustIcon(icons.HardwareDesktopWindows)
	iconMobile  = mustIcon(icons.HardwareSmartphone)
	iconWeb     = mustIcon(icons.SocialPublic)
	iconConsole = mustIcon(icons.HardwareVideogameAsset)
)

// platformIcon is a platform someone is connected from, with their status
// on it.
type platformIcon struct {
	icon   *widget.Icon
	status discord.Status
}

// platformsOf are the platforms p is connected from.
func platformsOf(p *discord.Presence) []platformIcon {
	if p == nil {
		return nil
	}
	var out []platformIcon
	for _, pl := range []platformIcon{
		{iconDesktop, p.ClientStatus.Desktop},
		{iconMobile, p.ClientStatus.Mobile},
		{iconWeb, p.ClientStatus.Web},
		{iconConsole, p.ClientStatus.Embedded},
	} {
		if pl.status != "" && pl.status != discord.OfflineStatus && pl.status != discord.InvisibleStatus {
			out = append(out, pl)
		}
	}
	return out
}

// layoutPlatforms are the icons of the platforms p is connected from, each
// in the color of its status.
func (c *chatView) layoutPlatforms(gtx layout.Context, p *discord.Presence) layout.Dimensions {
	if c.u.app.NoPlatformIcons {
		return layout.Dimensions{}
	}
	pls := platformsOf(p)
	if len(pls) == 0 {
		return layout.Dimensions{}
	}
	var children []layout.FlexChild
	for _, pl := range pls {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icon(gtx, pl.icon, 14, statusColor(pl.status))
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// staffRank is how high id stands in the server: the owner, an admin, one
// who manages it, or none.
type staffRank int

const (
	staffNone staffRank = iota
	staffManager
	staffAdmin
	staffOwner
)

// managePermissions are those of who manages a server, below its admins.
const managePermissions = discord.PermissionManageGuild | discord.PermissionManageChannels | discord.PermissionManageRoles |
	discord.PermissionKickMembers | discord.PermissionBanMembers | discord.PermissionModerateMembers | discord.PermissionManageMessages

// staffRankOf is how high id stands in guildID, by its owner and the
// permissions of their roles.
func (c *chatView) staffRankOf(guildID discord.GuildID, id discord.UserID) staffRank {
	if c.u.app.NoStaffTags || !guildID.IsValid() {
		return staffNone
	}
	g, err := c.st.Cabinet.Guild(guildID)
	if err != nil {
		return staffNone
	}
	if g.OwnerID == id {
		return staffOwner
	}
	m, err := c.st.Cabinet.Member(guildID, id)
	if err != nil {
		return staffNone
	}
	var perms discord.Permissions
	if everyone, err := c.st.Cabinet.Role(guildID, discord.RoleID(guildID)); err == nil {
		perms |= everyone.Permissions
	}
	for _, rid := range m.RoleIDs {
		if r, err := c.st.Cabinet.Role(guildID, rid); err == nil {
			perms |= r.Permissions
		}
	}
	switch {
	case perms.Has(discord.PermissionAdministrator):
		return staffAdmin
	case perms&managePermissions != 0:
		return staffManager
	}
	return staffNone
}

// Crowns: gold for the owner, silver for admins, bronze for who manages.
var (
	colorCrownOwner   = rgb(0xf0b232)
	colorCrownAdmin   = rgb(0xc0c7d1)
	colorCrownManager = rgb(0xc77d3a)
)

// layoutStaffTag is the crown of id's rank in guildID, or nothing.
func (c *chatView) layoutStaffTag(gtx layout.Context, guildID discord.GuildID, id discord.UserID) layout.Dimensions {
	var col colorT
	switch c.staffRankOf(guildID, id) {
	case staffOwner:
		col = colorCrownOwner
	case staffAdmin:
		col = colorCrownAdmin
	case staffManager:
		col = colorCrownManager
	default:
		return layout.Dimensions{}
	}
	return layout.Inset{Left: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return crown(gtx, gtx.Dp(14), col)
	})
}

// nameWithMarks lays out name, as wide as the room left by marks, then
// marks right after it, both centered on the same line.
func nameWithMarks(gtx layout.Context, name, marks layout.Widget) layout.Dimensions {
	mgtx := gtx
	mgtx.Constraints.Min = image.Point{}
	rec := op.Record(gtx.Ops)
	md := marks(mgtx)
	call := rec.Stop()
	ngtx := gtx
	ngtx.Constraints.Min = image.Point{}
	ngtx.Constraints.Max.X = max(gtx.Constraints.Max.X-md.Size.X, 0)
	nd := name(ngtx)
	h := max(nd.Size.Y, md.Size.Y)
	t := op.Offset(image.Pt(nd.Size.X, (h-md.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(nd.Size.X+md.Size.X, h), Baseline: nd.Baseline}
}

// crown draws a crown of s pixels in col.
func crown(gtx layout.Context, s int, col colorT) layout.Dimensions {
	w, h := float32(s), float32(s)
	var p clip.Path
	p.Begin(gtx.Ops)
	// A band at the bottom, three points over it.
	p.MoveTo(f32.Pt(w*0.08, h*0.82))
	p.LineTo(f32.Pt(w*0.04, h*0.28))
	p.LineTo(f32.Pt(w*0.30, h*0.52))
	p.LineTo(f32.Pt(w*0.50, h*0.14))
	p.LineTo(f32.Pt(w*0.70, h*0.52))
	p.LineTo(f32.Pt(w*0.96, h*0.28))
	p.LineTo(f32.Pt(w*0.92, h*0.82))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
	paint.FillShape(gtx.Ops, col, clip.Rect{Min: image.Pt(int(w*0.08), int(h*0.86)), Max: image.Pt(int(w*0.92), int(h*0.96))}.Op())
	return layout.Dimensions{Size: image.Pt(s, s)}
}
