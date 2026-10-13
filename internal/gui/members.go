package gui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/ningen/v3/states/member"
)

// memberRow is a line of the member list: the title of a group, or a member.
type memberRow struct {
	group string
	// member is nil for group titles.
	member *discord.Member
	// presence is the one the member list came with, if any.
	presence *discord.Presence
}

// membersShown reports whether the selected channel has a member list:
// server channels other than threads, and group messages.
func (c *chatView) membersShown() bool {
	ch := c.selChannel
	if ch == nil || !c.showMembers {
		return false
	}
	switch ch.Type {
	case discord.GroupDM:
		return true
	case discord.GuildText, discord.GuildAnnouncement:
		return true
	}
	return false
}

// requestMembers asks the gateway for the member list of the selected
// channel up to chunk, which arrives as GuildMemberListUpdate events.
func (c *chatView) requestMembers(chunk int) {
	ch := c.selChannel
	if c.demo || ch == nil || !ch.GuildID.IsValid() || chunk <= c.memberChunk && chunk != 0 {
		return
	}
	c.memberChunk = max(c.memberChunk, chunk)
	guildID, chID := ch.GuildID, ch.ID
	go c.st.MemberState.RequestMemberList(guildID, chID, chunk)
}

// rebuildMembers recomputes the member list of the selected channel.
func (c *chatView) rebuildMembers() {
	c.memberRows = c.memberRows[:0]
	ch := c.selChannel
	if ch == nil {
		return
	}
	if ch.Type == discord.GroupDM {
		users := slices.Clone(ch.DMRecipients)
		users = append(users, c.me)
		slices.SortFunc(users, func(a, b discord.User) int {
			return cmp.Or(
				cmp.Compare(statusRank(c.status(discord.NullGuildID, a.ID)), statusRank(c.status(discord.NullGuildID, b.ID))),
				strings.Compare(strings.ToLower(a.DisplayOrUsername()), strings.ToLower(b.DisplayOrUsername())),
			)
		})
		c.memberRows = append(c.memberRows, memberRow{group: fmt.Sprintf("MEMBERS — %d", len(users))})
		for _, u := range users {
			c.memberRows = append(c.memberRows, memberRow{member: &discord.Member{User: u}})
		}
		return
	}
	if !ch.GuildID.IsValid() {
		return
	}
	if c.demo {
		c.demoMembers(ch.GuildID)
		return
	}
	list, err := c.st.MemberState.GetMemberList(ch.GuildID, ch.ID)
	if err != nil {
		// The list has yet to arrive.
		return
	}
	// The groups inside the items often come without their count, which the
	// list keeps apart.
	counts := make(map[string]uint64)
	list.ViewGroups(func(groups []gateway.GuildMemberListGroup) {
		for _, g := range groups {
			counts[g.ID] = g.Count
		}
	})
	list.ViewItems(func(items []gateway.GuildMemberListOpItem) {
		for _, item := range items {
			switch {
			case item.Group != nil:
				g := *item.Group
				if g.Count == 0 {
					g.Count = counts[g.ID]
				}
				c.memberRows = append(c.memberRows, memberRow{group: c.groupName(ch.GuildID, g)})
			case item.Member != nil:
				m := item.Member.Member
				p := item.Member.Presence
				c.memberRows = append(c.memberRows, memberRow{member: &m, presence: &p})
			}
		}
	})
}

// demoMembers groups the members of the demo as Discord does: by their
// highest hoisted role while online, then the offline ones.
func (c *chatView) demoMembers(guildID discord.GuildID) {
	members, _ := c.st.Cabinet.Members(guildID)
	groups := make(map[string][]discord.Member)
	var order []discord.Role
	for _, m := range members {
		key := "online"
		if s := c.status(guildID, m.User.ID); s == discord.OfflineStatus || s == discord.InvisibleStatus {
			key = "offline"
		} else {
			var best *discord.Role
			for _, id := range m.RoleIDs {
				if r, err := c.st.Cabinet.Role(guildID, id); err == nil && r.Hoist && (best == nil || r.Position > best.Position) {
					best = r
				}
			}
			if best != nil {
				key = best.ID.String()
				if !slices.ContainsFunc(order, func(r discord.Role) bool { return r.ID == best.ID }) {
					order = append(order, *best)
				}
			}
		}
		groups[key] = append(groups[key], m)
	}
	slices.SortFunc(order, func(a, b discord.Role) int { return cmp.Compare(b.Position, a.Position) })
	keys := make([]string, 0, len(order)+2)
	for _, r := range order {
		keys = append(keys, r.ID.String())
	}
	keys = append(keys, "online", "offline")
	for _, k := range keys {
		ms := groups[k]
		if len(ms) == 0 {
			continue
		}
		slices.SortFunc(ms, func(a, b discord.Member) int {
			return strings.Compare(strings.ToLower(cmp.Or(a.Nick, a.User.DisplayOrUsername())), strings.ToLower(cmp.Or(b.Nick, b.User.DisplayOrUsername())))
		})
		c.memberRows = append(c.memberRows, memberRow{group: c.groupName(guildID, gateway.GuildMemberListGroup{ID: k, Count: uint64(len(ms))})})
		for i := range ms {
			c.memberRows = append(c.memberRows, memberRow{member: &ms[i]})
		}
	}
}

func (c *chatView) groupName(guildID discord.GuildID, g gateway.GuildMemberListGroup) string {
	name := "MEMBERS"
	switch g.ID {
	case "online":
		name = "ONLINE"
	case "offline":
		name = "OFFLINE"
	default:
		if id, err := discord.ParseSnowflake(g.ID); err == nil {
			if role, err := c.st.Cabinet.Role(guildID, discord.RoleID(id)); err == nil {
				name = strings.ToUpper(role.Name)
			}
		}
	}
	return fmt.Sprintf("%s — %d", name, g.Count)
}

func (c *chatView) layoutMembers(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	guildID := c.selChannel.GuildID
	if len(c.memberRows) == 0 {
		return layout.UniformInset(unit.Dp(16)).Layout(gtx, label(th, 13, colorMuted, "Loading members…").Layout)
	}
	dims := listLayout(gtx, th, &c.memberList, len(c.memberRows), func(gtx layout.Context, i int) layout.Dimensions {
		r := c.memberRows[i]
		if r.member == nil {
			top := unit.Dp(20)
			if i == 0 {
				top = 16
			}
			return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8), Top: top, Bottom: unit.Dp(4)}.Layout(gtx,
				oneLine(bold(label(th, 12, colorMuted, r.group))).Layout)
		}
		m := r.member
		status := c.status(guildID, m.User.ID)
		presence := c.presence(guildID, m.User.ID)
		if presence == nil {
			presence = r.presence
			if presence != nil && presence.Status != "" && m.User.ID != c.me.ID {
				status = presence.Status
			}
		}
		name := cmp.Or(m.Nick, m.User.DisplayOrUsername())
		nameColor := colorText
		if guildID.IsValid() {
			if col, ok := state.MemberColor(m, func(id discord.RoleID) *discord.Role {
				role, _ := c.st.Cabinet.Role(guildID, id)
				return role
			}); ok && col != 0 && col != discord.NullColor {
				nameColor = rgb(uint32(col))
			}
		}
		offline := status == discord.OfflineStatus || status == discord.InvisibleStatus
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, c.userClick(m.User.ID), func(gtx layout.Context, hovered bool) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						url := cdnSize(m.User.AvatarURLWithType(discord.PNGImage))
						if guildID.IsValid() && m.Avatar != "" {
							url = cdnSize(m.AvatarURLWithType(discord.PNGImage, guildID))
						}
						bg := colorSidebar
						if hovered {
							bg = colorHover
						}
						dims := c.u.picture(gtx, url, 32, colorFor(uint64(m.User.ID)), name, -1)
						statusDot(gtx, status, dims.Size.X, bg)
						return dims
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						col := nameColor
						if offline && !hovered {
							col = withAlpha(col, 0x80)
						}
						rows := []layout.FlexChild{layout.Rigid(oneLine(label(th, 15, col, name)).Layout)}
						if line := activityLine(presence); line != "" && !offline {
							rows = append(rows, layout.Rigid(oneLine(label(th, 12, colorMuted, line)).Layout))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					}),
				)
			}, false)
		})
	})
	// Ask for the members below the ones shown before they are reached.
	pos := c.memberList.Position
	c.requestMembers(member.ChunkFromIndex(pos.First+pos.Count) + 1)
	return dims
}
