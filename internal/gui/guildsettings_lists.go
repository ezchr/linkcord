package gui

import (
	"cmp"
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// gsLists are the pages of a server's settings that list things: members,
// roles, invites, the audit log, bans and AutoMod's rules.
type gsLists struct {
	// Members.
	memberSearch   widget.Editor
	memberPage     int
	prevPage, next widget.Clickable
	memberMenu     map[discord.UserID]*widget.Clickable

	// Roles: role is the one being edited, and roleDrag the one being
	// dragged, in the order of roleOrder.
	roleSearch           widget.Editor
	createRole, everyone widget.Clickable
	roleEdit, roleDelete map[discord.RoleID]*widget.Clickable
	role                 roleEditor
	roleOrder            []discord.Role
	roleDrag             roleDrag
	roleDragTags         map[discord.RoleID]*widget.Clickable

	// Invites.
	invites             fetched[[]gsInvite]
	createInvite, pause widget.Clickable
	revoke              map[string]*widget.Clickable
	pausedUntil         *time.Time
	pauseAsking         bool

	// Audit Log.
	audit                fetched[gsAuditLog]
	auditUser, auditKind widget.Clickable
	auditUserID          discord.UserID
	auditAction          int

	// Bans.
	bans       fetched[[]discord.Ban]
	banSearch  widget.Editor
	banFind    widget.Clickable
	banFilter  string
	unban      map[discord.UserID]*widget.Clickable
	automod    fetched[[]gsRule]
	ruleToggle map[int]*widget.Clickable
	ruleDelete map[int]*widget.Clickable
}

// roleDrag is a role being dragged in the list of roles.
type roleDrag struct {
	pressed, dragging bool
	from, target      int
	startY            float32
	rowH              int
}

type gsInvite struct {
	Code    string        `json:"code"`
	Inviter *discord.User `json:"inviter"`
	Channel struct {
		ID   discord.ChannelID `json:"id"`
		Name string            `json:"name"`
	} `json:"channel"`
	Uses      int        `json:"uses"`
	MaxUses   int        `json:"max_uses"`
	MaxAge    int        `json:"max_age"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type gsAuditLog struct {
	Users   []discord.User `json:"users"`
	Entries []struct {
		ID         discord.Snowflake `json:"id"`
		UserID     discord.UserID    `json:"user_id"`
		TargetID   discord.Snowflake `json:"target_id"`
		ActionType int               `json:"action_type"`
		Reason     string            `json:"reason"`
	} `json:"audit_log_entries"`
}

// gsRule is one of AutoMod's rules.
type gsRule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TriggerType int    `json:"trigger_type"`
	Enabled     bool   `json:"enabled"`
}

const membersPerPage = 12

func (c *chatView) loadGuildList(p gsPage) {
	gs := &c.gset
	l := &gs.lists
	id := gs.guildID.String()
	l.memberSearch.SingleLine, l.roleSearch.SingleLine = true, true
	l.banSearch.SingleLine, l.banSearch.Submit = true, true
	switch p {
	case gsInvites:
		l.invites.loaded = false
		fetch(c, &l.invites, "the invites", api.EndpointGuilds+id+"/invites", func() []gsInvite {
			exp := time.Now().Add(17 * time.Hour)
			return []gsInvite{{Code: "demo123", Inviter: &c.me, Uses: 3, ExpiresAt: &exp, Channel: struct {
				ID   discord.ChannelID `json:"id"`
				Name string            `json:"name"`
			}{Name: "general-chat"}}}
		})
		c.loadInvitePause()
	case gsAudit:
		c.loadAudit()
	case gsBans:
		l.bans.loaded = false
		fetch(c, &l.bans, "the bans", api.EndpointGuilds+id+"/bans?limit=1000", func() []discord.Ban { return nil })
	case gsAutoMod:
		l.automod.loaded = false
		fetch(c, &l.automod, "AutoMod's rules", api.EndpointGuilds+id+"/auto-moderation/rules", func() []gsRule {
			return []gsRule{{ID: "1", Name: "Block Mention Spam", TriggerType: 5, Enabled: true}}
		})
	case gsSafety:
		c.loadSafetyChannel()
	}
}

// loadAudit asks for the audit log with the filters chosen.
func (c *chatView) loadAudit() {
	gs := &c.gset
	l := &gs.lists
	q := "?limit=50"
	if l.auditUserID.IsValid() {
		q += "&user_id=" + l.auditUserID.String()
	}
	if l.auditAction != 0 {
		q += "&action_type=" + strconv.Itoa(l.auditAction)
	}
	l.audit.loaded, l.audit.err = false, ""
	fetch(c, &l.audit, "the audit log", api.EndpointGuilds+gs.guildID.String()+"/audit-logs"+q, func() gsAuditLog {
		var a gsAuditLog
		a.Users = []discord.User{c.me}
		a.Entries = append(a.Entries, struct {
			ID         discord.Snowflake `json:"id"`
			UserID     discord.UserID    `json:"user_id"`
			TargetID   discord.Snowflake `json:"target_id"`
			ActionType int               `json:"action_type"`
			Reason     string            `json:"reason"`
		}{ID: discord.NewSnowflake(time.Now().Add(-time.Hour)), UserID: c.me.ID, TargetID: discord.Snowflake(c.demoChannelID()), ActionType: 11})
		return a
	})
}

func (c *chatView) demoChannelID() discord.ChannelID {
	if chs := c.guildChannels(discord.GuildText); len(chs) > 0 {
		return chs[0].ID
	}
	return 0
}

// loadInvitePause asks whether the server's invites are paused.
func (c *chatView) loadInvitePause() {
	gs := &c.gset
	l := &gs.lists
	if c.demo || l.pauseAsking {
		return
	}
	l.pauseAsking = true
	guildID := gs.guildID
	go func() {
		g, err := c.st.Client.RequestJSON[struct {
			Incidents *struct {
				InvitesDisabledUntil *time.Time `json:"invites_disabled_until"`
			} `json:"incidents_data"`
		}]("GET", api.EndpointGuilds+guildID.String())
		c.u.post(func() {
			l.pauseAsking = false
			if err != nil || gs.guildID != guildID {
				return
			}
			l.pausedUntil = nil
			if g.Incidents != nil {
				l.pausedUntil = g.Incidents.InvitesDisabledUntil
			}
		})
	}()
}

func (l *gsLists) invitesPaused() bool {
	return l.pausedUntil != nil && l.pausedUntil.After(time.Now())
}

func (c *chatView) updateGuildLists(gtx layout.Context) {
	gs := &c.gset
	l := &gs.lists
	id := gs.guildID.String()

	// Members.
	if l.prevPage.Clicked(gtx) && l.memberPage > 0 {
		l.memberPage--
	}
	if l.next.Clicked(gtx) {
		l.memberPage++
	}
	for uid, click := range l.memberMenu {
		if click.Clicked(gtx) {
			c.openUserMenu(uid, gs.guildID, 0)
		}
	}

	// Roles.
	c.updateRoleEditor(gtx)
	if l.createRole.Clicked(gtx) {
		c.createRole()
	}
	if l.everyone.Clicked(gtx) {
		c.openRoleEditor(discord.RoleID(gs.guildID), rolePermissions)
	}
	for rid, click := range l.roleEdit {
		if click.Clicked(gtx) {
			c.openRoleEditor(rid, roleDisplay)
		}
	}
	// The role's menu.
	for rid, click := range l.roleDelete {
		if click.Clicked(gtx) {
			rid := rid
			r, err := c.st.Cabinet.Role(gs.guildID, rid)
			if err != nil {
				continue
			}
			editable := c.canEditRole(gs.guildID, *r)
			managed := r.Managed
			c.openMenu(c.pointerAt, 200, func() []menuItem {
				items := []menuItem{{text: map[bool]string{true: "Edit Role", false: "View Role"}[editable], act: func() { c.openRoleEditor(rid, roleDisplay) }},
					{text: "Copy Role ID", act: func() { c.copyText = rid.String() }}}
				if editable && !managed {
					items = append(items, menuItem{sep: true}, menuItem{text: "Delete Role", col: colorRed, act: func() { c.askDeleteRole(rid) }})
				}
				return items
			})
		}
	}

	// Invites.
	if l.createInvite.Clicked(gtx) {
		if ch, ok := c.inviteChannel(gs.guildID); ok {
			c.copyInvite(ch)
			l.invites.loaded = false
			c.loadGuildListLater(gsInvites)
		}
	}
	if l.pause.Clicked(gtx) {
		var until any
		if !l.invitesPaused() {
			t := time.Now().Add(24 * time.Hour).UTC()
			until = t.Format(time.RFC3339)
			l.pausedUntil = &t
		} else {
			l.pausedUntil = nil
		}
		c.guildRequest("PUT", api.EndpointGuilds+id+"/incident-actions", map[string]any{"invites_disabled_until": until, "dms_disabled_until": nil}, nil)
	}
	for code, click := range l.revoke {
		if click.Clicked(gtx) {
			code := code
			c.guildRequest("DELETE", api.EndpointInvites+code, nil, func() {
				l.invites.data = slices.DeleteFunc(l.invites.data, func(i gsInvite) bool { return i.Code == code })
			})
		}
	}

	// Audit Log.
	if l.auditUser.Clicked(gtx) {
		c.openMenu(c.pointerAt, 240, func() []menuItem {
			items := []menuItem{{key: "all", text: "All Users", check: checked(!l.auditUserID.IsValid()), act: func() { l.auditUserID = 0; c.loadAudit() }}}
			for _, u := range l.audit.data.Users {
				u := u
				items = append(items, menuItem{key: "u" + u.ID.String(), text: u.DisplayOrUsername(), check: checked(l.auditUserID == u.ID), act: func() {
					l.auditUserID = u.ID
					c.loadAudit()
				}})
			}
			return items
		})
	}
	if l.auditKind.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, func() []menuItem {
			items := []menuItem{{key: "all", text: "All Actions", check: checked(l.auditAction == 0), act: func() { l.auditAction = 0; c.loadAudit() }}}
			for _, k := range auditFilterKinds {
				k := k
				items = append(items, menuItem{key: fmt.Sprint("a", k), text: auditActionName(k), check: checked(l.auditAction == k), act: func() {
					l.auditAction = k
					c.loadAudit()
				}})
			}
			return items
		})
	}

	// Bans.
	if l.banFind.Clicked(gtx) {
		l.banFilter = strings.ToLower(strings.TrimSpace(l.banSearch.Text()))
	}
	for {
		e, ok := l.banSearch.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			l.banFilter = strings.ToLower(strings.TrimSpace(l.banSearch.Text()))
		}
	}
	for uid, click := range l.unban {
		if click.Clicked(gtx) {
			uid := uid
			c.guildRequest("DELETE", api.EndpointGuilds+id+"/bans/"+uid.String(), nil, func() {
				l.bans.data = slices.DeleteFunc(l.bans.data, func(b discord.Ban) bool { return b.User.ID == uid })
			})
		}
	}

	// AutoMod.
	for t, click := range l.ruleToggle {
		if !click.Clicked(gtx) {
			continue
		}
		t := t
		if i := slices.IndexFunc(l.automod.data, func(r gsRule) bool { return r.TriggerType == t }); i >= 0 {
			r := &l.automod.data[i]
			r.Enabled = !r.Enabled
			c.guildRequest("PATCH", api.EndpointGuilds+id+"/auto-moderation/rules/"+r.ID, map[string]any{"enabled": r.Enabled}, nil)
		} else {
			c.createAutoModRule(t)
		}
	}
	for t, click := range l.ruleDelete {
		if !click.Clicked(gtx) {
			continue
		}
		if i := slices.IndexFunc(l.automod.data, func(r gsRule) bool { return r.TriggerType == t }); i >= 0 {
			ruleID := l.automod.data[i].ID
			c.guildRequest("DELETE", api.EndpointGuilds+id+"/auto-moderation/rules/"+ruleID, nil, func() {
				l.automod.data = slices.DeleteFunc(l.automod.data, func(r gsRule) bool { return r.ID == ruleID })
			})
		}
	}
}

// loadGuildListLater asks for a list again in a moment, once Discord has
// what was just changed.
func (c *chatView) loadGuildListLater(p gsPage) {
	time.AfterFunc(time.Second, func() {
		c.u.post(func() {
			if c.gset.open && c.gset.page == p {
				c.loadGuildList(p)
			}
		})
	})
}

// autoModKinds are AutoMod's kinds of rule, as Discord's page lists them.
var autoModKinds = []struct {
	trigger     int
	title, info string
}{
	{5, "Block Mention Spam", "Block messages with an excessive amount of role and user mentions."},
	{3, "Block Suspected Spam Content", "Monitor messages, forum posts and threads for content or activity that may be spam."},
	{4, "Block Commonly Flagged Words", "Flag messages containing profanity, insults and more."},
	{1, "Block Custom Words", "Make your own filter to block specific language in your server."},
}

// createAutoModRule makes the rule of the kind trigger, as Discord's
// defaults, asking first for the words of a custom one.
func (c *chatView) createAutoModRule(trigger int) {
	gs := &c.gset
	endpoint := api.EndpointGuilds + gs.guildID.String() + "/auto-moderation/rules"
	block := []map[string]any{{"type": 1, "metadata": map[string]any{}}}
	send := func(name string, meta map[string]any) {
		body := map[string]any{"name": name, "event_type": 1, "trigger_type": trigger, "trigger_metadata": meta, "actions": block, "enabled": true}
		c.guildRequest("POST", endpoint, body, func() {
			c.gset.lists.automod.data = append(c.gset.lists.automod.data, gsRule{Name: name, TriggerType: trigger, Enabled: true})
			c.loadGuildListLater(gsAutoMod)
		})
	}
	switch trigger {
	case 5:
		send("Block Mention Spam", map[string]any{"mention_total_limit": 20})
	case 3:
		send("Block Suspected Spam Content", map[string]any{})
	case 4:
		send("Block Commonly Flagged Words", map[string]any{"presets": []int{1, 2, 3}})
	case 1:
		fields := []*modalField{promptField("Words to block", "", "word, another word", 1000, true)}
		c.prompt("Block Custom Words", "Create", false, []string{"Separate the words with commas."}, fields, func(values []string) {
			var words []string
			for _, w := range strings.Split(values[0], ",") {
				if w = strings.TrimSpace(w); w != "" {
					words = append(words, w)
				}
			}
			if len(words) > 0 {
				send("Block Custom Words", map[string]any{"keyword_filter": words})
			}
		})
	}
}

// guildListRows are the pages that list.
func (c *chatView) guildListRows() []layout.Widget {
	switch c.gset.page {
	case gsMembers:
		return c.memberRowsPage()
	case gsRoles:
		return c.roleRows()
	case gsInvites:
		return c.inviteRows()
	case gsAudit:
		return c.auditRows()
	case gsBans:
		return c.banRows()
	case gsAutoMod:
		return c.autoModRows()
	}
	return nil
}

// joinedAgo is how long ago a member joined, if known.
func joinedAgo(t discord.Timestamp) string {
	if !t.IsValid() {
		return "—"
	}
	return ago(t.Time())
}

// ago is how long ago t was, as Discord's tables say it.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%d years ago", int(d.Hours()/24/365))
}

// tableRow lays out cells with the widths given, as fractions of the row.
func tableRow(gtx layout.Context, widths []float32, cells ...layout.Widget) layout.Dimensions {
	var children []layout.FlexChild
	for i, w := range cells {
		w := w
		children = append(children, layout.Flexed(widths[i], func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, w)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

func (c *chatView) memberRowsPage() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	members, _ := c.st.Cabinet.Members(gs.guildID)
	q := strings.ToLower(strings.TrimSpace(l.memberSearch.Text()))
	var list []discord.Member
	for _, m := range members {
		if q == "" || strings.Contains(strings.ToLower(m.User.Username), q) || strings.Contains(strings.ToLower(m.User.DisplayName), q) ||
			strings.Contains(strings.ToLower(m.Nick), q) || m.User.ID.String() == q {
			list = append(list, m)
		}
	}
	slices.SortFunc(list, func(a, b discord.Member) int { return b.Joined.Time().Compare(a.Joined.Time()) })
	pages := max((len(list)+membersPerPage-1)/membersPerPage, 1)
	l.memberPage = min(l.memberPage, pages-1)
	shown := list[min(l.memberPage*membersPerPage, len(list)):min((l.memberPage+1)*membersPerPage, len(list))]
	widths := []float32{3, 1.4, 1.4, 2, 0.4}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, bold(label(th, 15, colorHeader, "Recent Members")).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = gtx.Dp(260)
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return textField(gtx, th, &l.memberSearch, "Search by username or ID")
				}),
			)
		},
		layout.Spacer{Height: unit.Dp(10)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			h := func(t string) layout.Widget { return bold(label(th, 12, colorMuted, t)).Layout }
			return tableRow(gtx, widths, h("NAME"), h("MEMBER SINCE"), h("JOINED DISCORD"), h("ROLES"), h(""))
		},
		gap(6),
	}
	for _, m := range shown {
		m := m
		click := clickFor(&l.memberMenu, m.User.ID)
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				name := c.memberName(gs.guildID, m.User.ID, &m)
				col := colorHeader
				if cl, ok := c.memberColor(gs.guildID, m.User.ID); ok && cl != 0 && cl != discord.NullColor {
					col = rgb(uint32(cl))
				}
				return tableRow(gtx, widths,
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return c.u.picture(gtx, c.authorAvatarURL(gs.guildID, m.User), 32, colorFor(uint64(m.User.ID)), name, -1)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Flexed(1, stacked(oneLine(bold(label(th, 14, col, name))).Layout, oneLine(label(th, 12, colorMuted, m.User.Username)).Layout)),
						)
					},
					label(th, 13, colorText, joinedAgo(m.Joined)).Layout,
					label(th, 13, colorText, ago(m.User.ID.Time())).Layout,
					func(gtx layout.Context) layout.Dimensions {
						if len(m.RoleIDs) == 0 {
							return layout.Dimensions{}
						}
						var roles []discord.Role
						for _, id := range m.RoleIDs {
							if r, err := c.st.Cabinet.Role(gs.guildID, id); err == nil {
								roles = append(roles, *r)
							}
						}
						if len(roles) == 0 {
							return layout.Dimensions{}
						}
						slices.SortFunc(roles, func(a, b discord.Role) int { return b.Position - a.Position })
						more := ""
						if len(roles) > 1 {
							more = fmt.Sprintf(" +%d", len(roles)-1)
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return c.layoutRoles(gtx, gs.guildID, []discord.RoleID{roles[0].ID})
							}),
							layout.Rigid(label(th, 12, colorMuted, more).Layout),
						)
					},
					func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, click, iconMore, colorMuted, colorHeader)
					},
				)
			})
		})
	}
	rows = append(rows,
		gap(8),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 13, colorMuted, fmt.Sprintf("%d members loaded · page %d of %d", len(list), l.memberPage+1, pages)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &l.prevPage, colorSecondaryButton, "‹ Back")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &l.next, colorSecondaryButton, "Next ›")
				}),
			)
		},
	)
	return rows
}

func (c *chatView) roleRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	if l.role.id != 0 {
		return c.roleEditorRows()
	}
	roles, _ := c.st.Cabinet.Roles(gs.guildID)
	members, _ := c.st.Cabinet.Members(gs.guildID)
	count := map[discord.RoleID]int{}
	for _, m := range members {
		for _, id := range m.RoleIDs {
			count[id]++
		}
	}
	var all []discord.Role
	for _, r := range roles {
		if r.ID != discord.RoleID(gs.guildID) {
			all = append(all, r)
		}
	}
	slices.SortFunc(all, func(a, b discord.Role) int {
		return cmp.Or(cmp.Compare(b.Position, a.Position), cmp.Compare(a.ID, b.ID))
	})
	l.roleOrder = all
	q := strings.ToLower(strings.TrimSpace(l.roleSearch.Text()))
	rows := []layout.Widget{
		settingsText(th, "Use roles to group your server members and assign permissions."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		// @everyone's permissions, as Discord's card.
		func(gtx layout.Context) layout.Dimensions {
			return l.everyone.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				bg := colorSidebar
				if l.everyone.Hovered() {
					bg = colorHover
				}
				return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMembers, 24, colorText) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
							layout.Flexed(1, stacked(bold(label(th, 15, colorHeader, "Default Permissions")).Layout,
								label(th, 13, colorMuted, "@everyone · applies to all server members").Layout)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconChevron, 20, colorMuted) }),
						)
					})
				})
			})
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &l.roleSearch, "Search Roles") }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &l.createRole, colorAccent, "Create Role")
				}),
			)
		},
		layout.Spacer{Height: unit.Dp(8)}.Layout,
		settingsText(th, "Members use the color of the highest role they have on this list. Drag roles to reorder them."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			h := func(t string) layout.Widget { return bold(label(th, 12, colorMuted, t)).Layout }
			return tableRow(gtx, []float32{3, 1, 1}, h(fmt.Sprintf("ROLES — %d", len(all))), h("MEMBERS"), h(""))
		},
		gap(6),
	}
	for i, r := range all {
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		i, r := i, r
		editable := c.canEditRole(gs.guildID, r)
		edit, more := clickFor(&l.roleEdit, r.ID), clickFor(&l.roleDelete, r.ID)
		col := rgb(0x99aab5)
		if r.Color != 0 {
			col = rgb(uint32(r.Color))
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			dims := layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return tableRow(gtx, []float32{3, 1, 1},
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuPrivacy, 22, col) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if editable {
									return layout.Dimensions{}
								}
								return label(th, 13, colorMuted, "🔒").Layout(gtx)
							}),
							layout.Flexed(1, oneLine(bold(label(th, 14, colorHeader, r.Name))).Layout),
						)
					},
					label(th, 14, colorText, fmt.Sprintf("%d 👤", count[r.ID])).Layout,
					func(gtx layout.Context) layout.Dimensions {
						ic := iconMenuEdit
						if !editable {
							ic = iconEye
						}
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return squareButton(gtx, edit, ic) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return squareButton(gtx, more, iconMore) }),
						)
					},
				)
			})
			c.roleDragRow(gtx, i, r, editable, dims)
			return dims
		})
	}
	return rows
}

// squareButton is a round-cornered button with an icon, as the roles'.
func squareButton(gtx layout.Context, click *widget.Clickable, ic *widget.Icon) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		bg := colorSidebar
		if click.Hovered() {
			bg = colorHover
		}
		size := image.Pt(gtx.Dp(38), gtx.Dp(36))
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 18, colorHeader) })
	})
}

// roleDragRow lets the row of role i be dragged to another place, if it
// may be moved, and shows where it would land.
func (c *chatView) roleDragRow(gtx layout.Context, i int, r discord.Role, editable bool, dims layout.Dimensions) {
	l := &c.gset.lists
	d := &l.roleDrag
	tag := clickFor(&l.roleDragTags, r.ID)
	if editable {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if !ok {
				continue
			}
			switch e.Kind {
			case pointer.Press:
				if e.Buttons == pointer.ButtonPrimary {
					*d = roleDrag{pressed: true, from: i, startY: e.Position.Y, rowH: dims.Size.Y, target: i}
				}
			case pointer.Drag:
				if !d.pressed || d.from != i {
					continue
				}
				dy := e.Position.Y - d.startY
				if !d.dragging && abs32(dy) > float32(gtx.Dp(6)) {
					d.dragging = true
				}
				if d.dragging {
					d.target = min(max(i+int(roundHalf(dy/float32(max(d.rowH, 1)))), 0), len(l.roleOrder)-1)
					gtx.Execute(op.InvalidateCmd{})
				}
			case pointer.Release:
				if d.dragging && d.from == i && d.target != i {
					c.moveRole(i, d.target)
				}
				*d = roleDrag{}
			case pointer.Cancel:
				*d = roleDrag{}
			}
		}
		area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
		pass := pointer.PassOp{}.Push(gtx.Ops)
		event.Op(gtx.Ops, tag)
		pass.Pop()
		area.Pop()
	}
	if !d.dragging {
		return
	}
	switch {
	case i == d.from:
		paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x10), clip.Rect{Max: dims.Size}.Op())
	case i == d.target:
		y := 0
		if d.target > d.from {
			y = dims.Size.Y - gtx.Dp(2)
		}
		col := colorGreen
		if !c.roleMayGo(d.target) {
			col = colorRed
		}
		paint.FillShape(gtx.Ops, col, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(dims.Size.X, y+gtx.Dp(2))}.Op())
	}
}

func roundHalf(v float32) float32 {
	if v < 0 {
		return v - 0.5
	}
	return v + 0.5
}

// roleMayGo reports whether a role may land at index to of the list: not
// above the user's highest role, unless they own the server.
func (c *chatView) roleMayGo(to int) bool {
	gs := &c.gset
	l := &gs.lists
	if c.demo || gs.guild.OwnerID == c.me.ID {
		return true
	}
	if to < 0 || to >= len(l.roleOrder) {
		return false
	}
	return l.roleOrder[to].Position < c.myTopRole(gs.guildID)
}

// moveRole puts role from of the list at to, and sends Discord the new
// positions.
func (c *chatView) moveRole(from, to int) {
	gs := &c.gset
	l := &gs.lists
	if !c.roleMayGo(to) {
		gs.err = "A role cannot go above your highest role."
		return
	}
	order := slices.Clone(l.roleOrder)
	r := order[from]
	order = slices.Delete(order, from, from+1)
	order = slices.Insert(order, to, r)
	type pos struct {
		ID       string `json:"id"`
		Position int    `json:"position"`
	}
	var body []pos
	for i, role := range order {
		role.Position = len(order) - i
		c.st.Cabinet.RoleSet(gs.guildID, &role, true)
		body = append(body, pos{role.ID.String(), role.Position})
	}
	c.guildRequest("PATCH", api.EndpointGuilds+gs.guildID.String()+"/roles", body, nil)
}

func (c *chatView) inviteRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	pause := "Pause Invites"
	if l.invitesPaused() {
		pause = "Resume Invites"
	}
	rows := []layout.Widget{
		settingsText(th, "Create an invite link anyone can use to join, or invite friends directly."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if l.invitesPaused() {
						return label(th, 13, colorYellow, "Invites are paused until "+formatTime(*l.pausedUntil)).Layout(gtx)
					}
					return bold(label(th, 12, colorMuted, "ACTIVE INVITE LINKS")).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &l.pause, colorSecondaryButton, pause)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &l.createInvite, colorAccent, "Create Invite Link")
				}),
			)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	rows = append(rows, l.invites.note(th)...)
	if !l.invites.loaded {
		return rows
	}
	widths := []float32{2.4, 1.6, 0.8, 1.4, 0.4}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		h := func(t string) layout.Widget { return bold(label(th, 12, colorMuted, t)).Layout }
		return tableRow(gtx, widths, h("INVITER"), h("INVITE CODE"), h("USES"), h("EXPIRES"), h(""))
	}, gap(6))
	if len(l.invites.data) == 0 {
		rows = append(rows, settingsText(th, "No invites yet."))
	}
	// The time left counts down.
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
		return layout.Dimensions{}
	})
	for _, inv := range l.invites.data {
		rows = append(rows, c.inviteRow(inv, widths, clickFor(&l.revoke, inv.Code)))
	}
	return rows
}

// inviteRow is an invite in a table of invites: who made it, for which
// channel, its code, uses and time left, and a button that revokes it.
func (c *chatView) inviteRow(inv gsInvite, widths []float32, click *widget.Clickable) layout.Widget {
	th := c.u.th
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			who := discord.User{Username: "Unknown"}
			if inv.Inviter != nil {
				who = *inv.Inviter
			}
			uses := strconv.Itoa(inv.Uses)
			if inv.MaxUses > 0 {
				uses += "/" + strconv.Itoa(inv.MaxUses)
			}
			expires := "∞"
			if inv.ExpiresAt != nil {
				left := max(time.Until(*inv.ExpiresAt), 0).Round(time.Second)
				expires = fmt.Sprintf("%02d:%02d:%02d:%02d", int(left.Hours())/24, int(left.Hours())%24, int(left.Minutes())%60, int(left.Seconds())%60)
			}
			return tableRow(gtx, widths,
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.u.picture(gtx, cdnSize(who.AvatarURLWithType(discord.PNGImage)), 28, colorFor(uint64(who.ID)), who.DisplayOrUsername(), -1)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, stacked(oneLine(label(th, 14, colorHeader, who.DisplayOrUsername())).Layout, oneLine(label(th, 12, colorMuted, "#"+inv.Channel.Name)).Layout)),
					)
				},
				oneLine(label(th, 13, colorText, inv.Code)).Layout,
				label(th, 13, colorText, uses).Layout,
				label(th, 13, colorText, expires).Layout,
				func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, click, iconClose, colorMuted, colorRed)
				},
			)
		})
	}
}

// auditFilterKinds are the actions the audit log can be filtered by.
var auditFilterKinds = []int{1, 10, 11, 12, 20, 22, 23, 24, 25, 26, 27, 30, 31, 32, 40, 42, 72, 74, 75, 140}

// auditActionName is what an action of the audit log is.
func auditActionName(t int) string {
	if name, ok := auditActions[t]; ok {
		return name
	}
	return fmt.Sprintf("Action %d", t)
}

var auditActions = map[int]string{
	1: "Update Server", 10: "Create Channel", 11: "Update Channel", 12: "Delete Channel",
	13: "Create Channel Permission", 14: "Update Channel Permission", 15: "Delete Channel Permission",
	20: "Kick Member", 21: "Prune Members", 22: "Ban Member", 23: "Unban Member", 24: "Update Member",
	25: "Update Member Roles", 26: "Move Member", 27: "Disconnect Member", 28: "Add Bot",
	30: "Create Role", 31: "Update Role", 32: "Delete Role", 40: "Create Invite", 41: "Update Invite", 42: "Delete Invite",
	50: "Create Webhook", 51: "Update Webhook", 52: "Delete Webhook", 60: "Create Emoji", 61: "Update Emoji", 62: "Delete Emoji",
	72: "Delete Message", 73: "Bulk Delete Messages", 74: "Pin Message", 75: "Unpin Message",
	80: "Add Integration", 81: "Update Integration", 82: "Remove Integration",
	90: "Create Sticker", 91: "Update Sticker", 92: "Delete Sticker",
	100: "Create Event", 101: "Update Event", 102: "Delete Event",
	110: "Create Thread", 111: "Update Thread", 112: "Delete Thread",
	140: "Create AutoMod Rule", 141: "Update AutoMod Rule", 142: "Delete AutoMod Rule", 143: "AutoMod Blocked a Message",
	192: "Set Voice Channel Status", 193: "Delete Voice Channel Status",
}

// auditTarget names what an action of the audit log was done to.
func (c *chatView) auditTarget(t int, id discord.Snowflake, users []discord.User) string {
	switch {
	case t >= 10 && t <= 15, t >= 110 && t <= 112, t == 192 || t == 193:
		if ch, err := c.st.Cabinet.Channel(discord.ChannelID(id)); err == nil {
			return "#" + ch.Name
		}
	case t >= 20 && t <= 28, t == 72, t == 74, t == 75:
		for _, u := range users {
			if discord.Snowflake(u.ID) == id {
				return u.DisplayOrUsername()
			}
		}
	case t >= 30 && t <= 32:
		if r, err := c.st.Cabinet.Role(c.gset.guildID, discord.RoleID(id)); err == nil {
			return "@" + r.Name
		}
	}
	return ""
}

func (c *chatView) auditRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	userName := "All Users"
	for _, u := range l.audit.data.Users {
		if u.ID == l.auditUserID {
			userName = u.DisplayOrUsername()
		}
	}
	actionName := "All Actions"
	if l.auditAction != 0 {
		actionName = auditActionName(l.auditAction)
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, stacked(settingsHeading(th, "FILTER BY USER"), func(gtx layout.Context) layout.Dimensions { return dropdown(gtx, th, &l.auditUser, userName) })),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, stacked(settingsHeading(th, "FILTER BY ACTION"), func(gtx layout.Context) layout.Dimensions { return dropdown(gtx, th, &l.auditKind, actionName) })),
			)
		},
		gap(12),
	}
	rows = append(rows, l.audit.note(th)...)
	if !l.audit.loaded {
		return rows
	}
	users := l.audit.data.Users
	if len(l.audit.data.Entries) == 0 {
		rows = append(rows, settingsText(th, "Nothing here yet."))
	}
	for _, e := range l.audit.data.Entries {
		e := e
		who := discord.User{ID: e.UserID, Username: "Someone"}
		for _, u := range users {
			if u.ID == e.UserID {
				who = u
			}
		}
		text := auditActionName(e.ActionType)
		if target := c.auditTarget(e.ActionType, e.TargetID, users); target != "" {
			text += ": " + target
		}
		if e.Reason != "" {
			text += " — " + e.Reason
		}
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, cdnSize(who.AvatarURLWithType(discord.PNGImage)), 32, colorFor(uint64(who.ID)), who.DisplayOrUsername(), -1)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, stacked(
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(bold(label(th, 14, colorHeader, who.DisplayOrUsername())).Layout),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Flexed(1, label(th, 14, colorText, text).Layout),
						)
					},
					label(th, 12, colorMuted, formatTime(e.ID.Time())).Layout,
				)),
			)
		}))
	}
	return rows
}

func (c *chatView) banRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	rows := []layout.Widget{
		settingsText(th, "Bans are by account and IP address by default. A user can get around an IP ban by using a proxy."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return textField(gtx, th, &l.banSearch, "Search bans by user ID or username")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return button(gtx, th, &l.banFind, colorAccent, "Search") }),
			)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	rows = append(rows, l.bans.note(th)...)
	if !l.bans.loaded {
		return rows
	}
	var list []discord.Ban
	for _, b := range l.bans.data {
		f := l.banFilter
		if f == "" || strings.Contains(strings.ToLower(b.User.Username), f) || strings.Contains(strings.ToLower(b.User.DisplayName), f) || b.User.ID.String() == f {
			list = append(list, b)
		}
	}
	if len(list) == 0 {
		return append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(40), Bottom: unit.Dp(40)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.N.Layout(gtx, stacked(
					bold(label(th, 15, colorMuted, "NO BANNED USERS")).Layout,
					label(th, 13, colorMuted, "You haven't banned anybody.").Layout,
				))
			})
		}))
	}
	for _, b := range list {
		b := b
		click := clickFor(&l.unban, b.User.ID)
		reason := b.Reason
		if reason == "" {
			reason = "No reason given"
		}
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return cardRow(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return c.u.picture(gtx, cdnSize(b.User.AvatarURLWithType(discord.PNGImage)), 36, colorFor(uint64(b.User.ID)), b.User.DisplayOrUsername(), -1)
			}, b.User.DisplayOrUsername(), reason, func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, click, colorSecondaryButton, "Unban")
			})
		}))
	}
	return rows
}

func (c *chatView) autoModRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	l := &gs.lists
	rows := []layout.Widget{
		settingsText(th, "Give your moderators a break while keeping your server safe! Set up filters to moderate content and automate a response when it is found."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	rows = append(rows, l.automod.note(th)...)
	if !l.automod.loaded {
		return rows
	}
	rows = append(rows, section(th, "Content", ""))
	for _, k := range autoModKinds {
		k := k
		toggle, del := clickFor(&l.ruleToggle, k.trigger), clickFor(&l.ruleDelete, k.trigger)
		i := slices.IndexFunc(l.automod.data, func(r gsRule) bool { return r.TriggerType == k.trigger })
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, stacked(bold(label(th, 15, colorHeader, k.title)).Layout, label(th, 13, colorMuted, k.info).Layout)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if i < 0 {
						txt := "Set Up"
						if k.trigger == 1 {
							txt = "Create"
						}
						return button(gtx, th, toggle, colorAccent, txt)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return switchToggle(gtx, l.automod.data[i].Enabled) })
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return iconButton(gtx, del, iconDelete, colorMuted, colorRed)
						}),
					)
				}),
			)
		}))
	}
	g := &gs.guild
	f := contentFilters[min(int(g.ExplicitFilter), len(contentFilters)-1)]
	rows = append(rows,
		gap(16),
		section(th, "Sensitive Content Filters", "Choose if server members can share image-based media detected by Discord's sensitive media filters."),
		settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, stacked(bold(label(th, 15, colorHeader, f.name)).Layout, label(th, 13, colorMuted, f.about).Layout)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &gs.filterBtn, colorSecondaryButton, "Change")
				}),
			)
		}),
	)
	return rows
}
