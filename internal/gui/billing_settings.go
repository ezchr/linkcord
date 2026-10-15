package gui

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// The pages of blocked and ignored users, of Server Boosts and of Nitro,
// as Discord's settings have them. Each asks Discord when first shown.

// relEntry is a relationship as /users/@me/relationships gives it, which
// says, unlike the gateway's, whether the user is ignored.
type relEntry struct {
	ID          discord.UserID           `json:"id"`
	Type        discord.RelationshipType `json:"type"`
	User        discord.User             `json:"user"`
	UserIgnored bool                     `json:"user_ignored"`
}

// boostSlot is one of the user's Server Boosts, used on a server or not.
type boostSlot struct {
	ID             string     `json:"id"`
	Canceled       bool       `json:"canceled"`
	CooldownEndsAt *time.Time `json:"cooldown_ends_at"`
	Subscription   *struct {
		GuildID discord.GuildID `json:"guild_id"`
	} `json:"premium_guild_subscription"`
}

// subscription is something the user pays Discord for.
type subscription struct {
	ID               string     `json:"id"`
	Type             int        `json:"type"`
	Status           int        `json:"status"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
	Items            []struct {
		Quantity int `json:"quantity"`
	} `json:"items"`
}

// fetched is what a page asked Discord for.
type fetched[T any] struct {
	loading, loaded bool
	err             string
	data            T
}

// fetch asks Discord for a page's data once, in the background.
func fetch[T any](c *chatView, f *fetched[T], what, endpoint string, demo func() T) {
	if f.loading || f.loaded {
		return
	}
	if c.demo {
		f.data, f.loaded = demo(), true
		return
	}
	f.loading = true
	go func() {
		data, err := c.st.Client.RequestJSON[T]("GET", endpoint)
		c.u.post(func() {
			f.loading = false
			if err != nil {
				f.err = "Discord did not give " + what + ": " + err.Error()
				slog.Warn("could not load "+what, "err", err)
				return
			}
			f.data, f.loaded = data, true
		})
	}()
}

// note is what a page says while its data loads or failed to.
func (f *fetched[T]) note(th *material.Theme) []layout.Widget {
	switch {
	case f.err != "":
		return []layout.Widget{func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, f.err).Layout)
		}}
	case f.loading:
		return []layout.Widget{settingsText(th, "Loading…")}
	}
	return nil
}

// billingView is the state of the pages.
type billingView struct {
	rels  fetched[[]relEntry]
	slots fetched[[]boostSlot]
	subs  fetched[[]subscription]

	unblock, unignore map[discord.UserID]*widget.Clickable
	goGuild           map[discord.GuildID]*widget.Clickable
	getNitro, manage  widget.Clickable
	busy              map[discord.UserID]bool
	actErr            string
}

// loadBillingPage asks for what page p shows.
func (c *chatView) loadBillingPage(p settingsPage) {
	b := &c.settings.billing
	switch p {
	case pageBlocked:
		// Asked each time, as blocks change elsewhere.
		if !b.rels.loading {
			b.rels.loaded = false
		}
		fetch(c, &b.rels, "the blocked users", api.EndpointMe+"/relationships", func() []relEntry {
			var out []relEntry
			for _, r := range c.relationships() {
				out = append(out, relEntry{ID: r.UserID, Type: r.Type, User: r.User})
			}
			return out
		})
	case pageBoosts:
		fetch(c, &b.slots, "the Server Boosts", api.EndpointMe+"/guilds/premium/subscription-slots", func() []boostSlot {
			used := boostSlot{ID: "1"}
			used.Subscription = &struct {
				GuildID discord.GuildID `json:"guild_id"`
			}{GuildID: c.demoGuildID()}
			return []boostSlot{used, {ID: "2"}}
		})
	case pageNitro:
		fetch(c, &b.subs, "the subscriptions", api.EndpointMe+"/billing/subscriptions", func() []subscription {
			end := time.Now().AddDate(0, 0, 18)
			boosts := subscription{ID: "1", Type: 2, Status: 1, CurrentPeriodEnd: &end}
			boosts.Items = append(boosts.Items, struct {
				Quantity int `json:"quantity"`
			}{Quantity: 2})
			return []subscription{boosts}
		})
	}
}

// demoGuildID is the demo's most boosted server.
func (c *chatView) demoGuildID() discord.GuildID {
	var best discord.Guild
	guilds, _ := c.st.Cabinet.Guilds()
	for _, g := range guilds {
		if g.NitroBoosters >= best.NitroBoosters {
			best = g
		}
	}
	return best.ID
}

func (c *chatView) updateBilling(gtx layout.Context) {
	b := &c.settings.billing
	for id, click := range b.unblock {
		if click.Clicked(gtx) && !b.busy[id] {
			c.liftRelationship(id, false)
		}
	}
	for id, click := range b.unignore {
		if click.Clicked(gtx) && !b.busy[id] {
			c.liftRelationship(id, true)
		}
	}
	for id, click := range b.goGuild {
		if click.Clicked(gtx) {
			c.closeSettings()
			c.selectGuild(id)
		}
	}
	if b.getNitro.Clicked(gtx) {
		openLink("https://discord.com/nitro")
	}
	if b.manage.Clicked(gtx) {
		openLink("https://discord.com/app")
	}
}

// liftRelationship unblocks the user, or stops ignoring them.
func (c *chatView) liftRelationship(id discord.UserID, unignore bool) {
	b := &c.settings.billing
	// The lists show the blocked and the ignored, so what changed leaves its
	// list.
	done := func() {
		for i := range b.rels.data {
			if b.rels.data[i].ID != id {
				continue
			}
			if unignore {
				b.rels.data[i].UserIgnored = false
			} else {
				b.rels.data[i].Type = 0
			}
		}
	}
	if c.demo {
		if !unignore {
			for _, r := range b.rels.data {
				if r.ID == id {
					c.setBlocked(r.User, false)
				}
			}
		}
		done()
		return
	}
	if b.busy == nil {
		b.busy = map[discord.UserID]bool{}
	}
	b.busy[id] = true
	b.actErr = ""
	endpoint := api.EndpointMe + "/relationships/" + id.String()
	if unignore {
		endpoint += "/ignore"
	}
	go func() {
		err := c.st.Client.FastRequest("DELETE", endpoint)
		c.u.post(func() {
			delete(b.busy, id)
			if err != nil {
				b.actErr = "Could not change it: " + err.Error()
				return
			}
			done()
		})
	}()
}

// settingsCard is a rounded box of a settings page, as Discord's.
func settingsCard(w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(12)).Layout(gtx, w)
			})
		})
	}
}

// cardRow is a row of a card: a picture, a name over a line, and a button.
func cardRow(gtx layout.Context, th *material.Theme, lead layout.Widget, name, sub string, end layout.Widget) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(lead),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Flexed(1, stacked(
			oneLine(bold(label(th, 15, colorHeader, name))).Layout,
			oneLine(label(th, 13, colorMuted, sub)).Layout,
		)),
		layout.Rigid(end),
	)
}

func clickFor[K comparable](m *map[K]*widget.Clickable, k K) *widget.Clickable {
	if *m == nil {
		*m = map[K]*widget.Clickable{}
	}
	if (*m)[k] == nil {
		(*m)[k] = new(widget.Clickable)
	}
	return (*m)[k]
}

// blockedRows are the page of blocked and ignored users.
func (c *chatView) blockedRows() []layout.Widget {
	th := c.u.th
	b := &c.settings.billing
	rows := b.rels.note(th)
	if b.actErr != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, b.actErr).Layout)
		})
	}
	if !b.rels.loaded {
		return rows
	}
	section := func(title, about, action, empty string, has func(relEntry) bool, clicks *map[discord.UserID]*widget.Clickable) {
		var list []relEntry
		for _, r := range b.rels.data {
			if has(r) {
				list = append(list, r)
			}
		}
		slices.SortFunc(list, func(a, b relEntry) int {
			return strings.Compare(strings.ToLower(a.User.DisplayOrUsername()), strings.ToLower(b.User.DisplayOrUsername()))
		})
		rows = append(rows,
			bold(label(th, 12, colorMuted, fmt.Sprintf("%s — %d", strings.ToUpper(title), len(list)))).Layout,
			settingsText(th, about),
			layout.Spacer{Height: unit.Dp(8)}.Layout,
		)
		if len(list) == 0 {
			rows = append(rows, settingsCard(label(th, 14, colorMuted, empty).Layout))
		}
		for _, r := range list {
			r := r
			click := clickFor(clicks, r.ID)
			rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
				return cardRow(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, cdnSize(r.User.AvatarURLWithType(discord.PNGImage)), 36, colorFor(uint64(r.ID)), r.User.DisplayOrUsername(), -1)
				}, r.User.DisplayOrUsername(), r.User.Username, func(gtx layout.Context) layout.Dimensions {
					txt := action
					if b.busy[r.ID] {
						txt = "…"
					}
					return button(gtx, th, click, colorSecondaryButton, txt)
				})
			}))
		}
		rows = append(rows, layout.Spacer{Height: unit.Dp(20)}.Layout)
	}
	section("Blocked Users", "They cannot message you, and their messages are hidden.", "Unblock", "You have not blocked anyone.",
		func(r relEntry) bool { return r.Type == discord.BlockedRelationship }, &b.unblock)
	section("Ignored Users", "Their messages and activity are quieter, without them knowing.", "Stop Ignoring", "You are not ignoring anyone.",
		func(r relEntry) bool { return r.UserIgnored }, &b.unignore)
	return rows
}

// levelPerks are what a server at level has from its boosts.
func levelPerks(level int) string {
	if level == 0 {
		return "No perks yet."
	}
	return strings.Join(boostPerks[level-1], ", ") + "."
}

// boostRows are the Server Boost page: the user's boosts and the servers
// they boost.
func (c *chatView) boostRows() []layout.Widget {
	th := c.u.th
	b := &c.settings.billing
	rows := b.slots.note(th)
	if !b.slots.loaded {
		return rows
	}
	unused := 0
	count := map[discord.GuildID]int{}
	var guilds []discord.GuildID
	for _, s := range b.slots.data {
		if s.Subscription == nil || !s.Subscription.GuildID.IsValid() {
			unused++
			continue
		}
		id := s.Subscription.GuildID
		if count[id] == 0 {
			guilds = append(guilds, id)
		}
		count[id]++
	}
	summary := "You have no Server Boosts. They come with Nitro, or can be bought on their own."
	if n := len(b.slots.data); n > 0 {
		summary = fmt.Sprintf("You have %d Server Boost%s, %d unused.", n, plural(n), unused)
	}
	rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, stacked(
				bold(label(th, 16, colorHeader, "Your Server Boosts")).Layout,
				label(th, 14, colorMuted, summary).Layout,
			)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, &b.getNitro, colorAccent, "Get Nitro")
			}),
		)
	}), layout.Spacer{Height: unit.Dp(16)}.Layout)
	if len(guilds) > 0 {
		rows = append(rows, bold(label(th, 12, colorMuted, "SERVERS YOU BOOST")).Layout, layout.Spacer{Height: unit.Dp(8)}.Layout)
	}
	for _, id := range guilds {
		id := id
		g, err := c.st.Cabinet.Guild(id)
		name, icon := "A server you are not in", ""
		var boosts uint64
		if err == nil {
			name, icon, boosts = g.Name, cdnSize(g.IconURLWithType(discord.PNGImage)), g.NitroBoosters
		}
		level, next := boostGoal(boosts)
		sub := fmt.Sprintf("Your %d boost%s · Level %d, %d boosts", count[id], plural(count[id]), level, boosts)
		if next > 0 {
			sub += fmt.Sprintf(", %d more for Level %d", next-boosts, level+1)
		}
		click := clickFor(&b.goGuild, id)
		rows = append(rows, settingsCard(stacked(
			func(gtx layout.Context) layout.Dimensions {
				return cardRow(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, icon, 40, colorServers, name, gtx.Dp(12))
				}, name, sub, func(gtx layout.Context) layout.Dimensions {
					if err != nil {
						return layout.Dimensions{}
					}
					return button(gtx, th, click, colorSecondaryButton, "Go to Server")
				})
			},
			layout.Spacer{Height: unit.Dp(8)}.Layout,
			label(th, 13, colorText, levelPerks(level)).Layout,
		)))
	}
	rows = append(rows, layout.Spacer{Height: unit.Dp(16)}.Layout, bold(label(th, 12, colorMuted, "WHAT BOOSTS UNLOCK")).Layout, layout.Spacer{Height: unit.Dp(8)}.Layout)
	for i, need := range boostLevels {
		lvl := i + 1
		rows = append(rows, settingsCard(stacked(
			bold(label(th, 15, colorHeader, fmt.Sprintf("Level %d · %d boosts", lvl, need))).Layout,
			label(th, 13, colorText, levelPerks(lvl)).Layout,
		)))
	}
	return rows
}

func plural[N int | uint64](n N) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// nitroName is what the user's Nitro is called.
func nitroName(n discord.UserNitro) string {
	switch n {
	case discord.NitroClassic:
		return "Nitro Classic"
	case discord.NitroFull:
		return "Nitro"
	case discord.NitroBasic:
		return "Nitro Basic"
	}
	return ""
}

// subscriptionStatus is how a subscription stands, and its color.
func subscriptionStatus(s subscription) (string, colorT) {
	end := ""
	if s.CurrentPeriodEnd != nil {
		end = s.CurrentPeriodEnd.Local().Format("January 2, 2006")
	}
	switch s.Status {
	case 1:
		if end != "" {
			return "Active, renews on " + end, colorGreen
		}
		return "Active", colorGreen
	case 2:
		return "Payment past due", colorYellow
	case 3:
		if end != "" {
			return "Canceled, ends on " + end, colorYellow
		}
		return "Canceled", colorYellow
	case 6:
		return "On hold for a payment", colorRed
	case 0:
		return "Waiting for payment", colorYellow
	}
	return "Ended", colorMuted
}

// nitroRows are the Subscriptions page: the user's Nitro and what else
// they pay for.
func (c *chatView) nitroRows() []layout.Widget {
	th := c.u.th
	b := &c.settings.billing
	nitro := nitroName(c.me.Nitro)
	title, about := "You do not have Nitro", "Nitro brings bigger uploads, HD streams, emoji anywhere, profile themes, Server Boosts and more."
	if nitro != "" {
		title, about = "You have "+nitro, "Thanks for supporting Discord."
	}
	rows := []layout.Widget{settingsCard(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, stacked(
				bold(label(th, 18, colorHeader, title)).Layout,
				label(th, 14, colorMuted, about).Layout,
			)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt := "Get Nitro"
				if nitro != "" {
					txt = "See Plans"
				}
				return button(gtx, th, &b.getNitro, colorAccent, txt)
			}),
		)
	}), layout.Spacer{Height: unit.Dp(16)}.Layout}
	// What it lets the user send, which Linkcord holds them to.
	rows = append(rows, settingsCard(stacked(
		bold(label(th, 15, colorHeader, "What you can send")).Layout,
		label(th, 14, colorMuted, fmt.Sprintf(tr("Files up to %s, or more in boosted servers · Messages up to %d characters; longer text is sent as message.txt"),
			formatSize(uint64(nitroUpload(c.me.Nitro))), c.messageLimit())).Layout,
	)), layout.Spacer{Height: unit.Dp(16)}.Layout)
	rows = append(rows, b.subs.note(th)...)
	if !b.subs.loaded {
		return rows
	}
	rows = append(rows, bold(label(th, 12, colorMuted, "SUBSCRIPTIONS")).Layout, layout.Spacer{Height: unit.Dp(8)}.Layout)
	if len(b.subs.data) == 0 {
		rows = append(rows, settingsCard(label(th, 14, colorMuted, "You have no subscriptions.").Layout))
	}
	for _, s := range b.subs.data {
		name := "App subscription"
		switch s.Type {
		case 1:
			name = cmp.Or(nitro, "Nitro")
		case 2:
			n := 0
			for _, it := range s.Items {
				n += it.Quantity
			}
			name = fmt.Sprintf("%d Server Boost%s", n, plural(n))
		}
		status, col := subscriptionStatus(s)
		rows = append(rows, settingsCard(stacked(
			bold(label(th, 15, colorHeader, name)).Layout,
			label(th, 13, col, status).Layout,
		)))
	}
	rows = append(rows,
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		settingsText(th, "Payments, plans and cancelling are changed in Discord's own app or website."),
		layout.Spacer{Height: unit.Dp(8)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, &b.manage, colorSecondaryButton, "Open Discord in the Browser")
			}))
		},
	)
	return rows
}
