package gui

import (
	"encoding/base64"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ncruces/zenity"
)

// The pages of a server's settings, as Discord's.
type gsPage int

const (
	gsProfile gsPage = iota
	gsEngagement
	gsMembers
	gsRoles
	gsInvites
	gsAccess
	gsSafety
	gsAudit
	gsBans
	gsAutoMod
	gsPages
)

var gsPageNames = [gsPages]string{
	"Server Profile", "Engagement", "Members", "Roles", "Invites", "Access",
	"Safety Setup", "Audit Log", "Bans", "AutoMod",
}

// guildSettings is the screen of a server's settings, over the whole window.
type guildSettings struct {
	open, focused bool
	guildID       discord.GuildID
	// guild is the server as last saved, changed here before Discord says.
	guild    discord.Guild
	page     gsPage
	pages    [gsPages]widget.Clickable
	list     widget.List
	closeBtn widget.Clickable
	err      string
	info     string

	// Server Profile.
	name, desc             widget.Editor
	origName, origDesc     string
	iconBtn, iconRemove    widget.Clickable
	save, reset            widget.Clickable
	saving                 bool
	bannerBtns             [10]widget.Clickable
	brand                  fetched[guildProfile]
	sysFlags               [4]widget.Clickable
	sysChan, afkChan, afkT widget.Clickable
	notify                 [2]widget.Clickable
	widgetBtn              widget.Clickable
	widget                 fetched[guildWidget]

	// Access and Safety Setup.
	ageBtn, rulesBtn, addRule widget.Clickable
	rule                      widget.Editor
	exampleRules              [4]widget.Clickable
	removeRule                map[int]*widget.Clickable
	rules                     fetched[memberVerification]
	verifBtn, filterBtn       widget.Clickable
	mfaBtn, alertsBtn         widget.Clickable
	safetyChannel             discord.ChannelID

	lists gsLists
}

// guildWidget is the server's widget for websites.
type guildWidget struct {
	Enabled bool `json:"enabled"`
}

// memberVerification is the server's rules, which members accept first.
type memberVerification struct {
	FormFields []struct {
		FieldType string   `json:"field_type"`
		Label     string   `json:"label"`
		Values    []string `json:"values"`
		Required  bool     `json:"required"`
	} `json:"form_fields"`
	Description string `json:"description"`
}

// bannerPresets are the colors of the server profile's banner, as
// Discord's.
var bannerPresets = [10]uint32{0x3b3232, 0xff3d9a, 0xe7151d, 0xf2711c, 0xf4d33b, 0x8a3fbf, 0x0a9dff, 0x3fe0c5, 0x4c7a12, 0x4a4a4a}

// openGuildSettings opens the settings of the server.
func (c *chatView) openGuildSettings(guildID discord.GuildID) {
	g, err := c.st.Cabinet.Guild(guildID)
	if err != nil {
		return
	}
	c.closeMenu()
	c.profile = nil
	gs := &c.gset
	*gs = guildSettings{open: true, guildID: guildID, guild: *g}
	gs.list.Axis = layout.Vertical
	gs.name.SingleLine = true
	gs.rule.SingleLine, gs.rule.Submit = true, true
	gs.origName, gs.origDesc = g.Name, g.Description
	gs.name.SetText(g.Name)
	gs.desc.SetText(g.Description)
	c.showGuildPage(gsProfile)
}

func (c *chatView) showGuildPage(p gsPage) {
	gs := &c.gset
	gs.page, gs.err, gs.info = p, "", ""
	gs.list.Position = layout.Position{}
	id := gs.guildID.String()
	switch p {
	case gsProfile:
		fetch(c, &gs.brand, "the server's profile", api.EndpointGuilds+id+"/profile", func() guildProfile { return guildProfile{BrandColor: "#e7151d"} })
	case gsEngagement:
		fetch(c, &gs.widget, "the server's widget", api.EndpointGuilds+id+"/widget", func() guildWidget { return guildWidget{} })
	case gsAccess:
		fetch(c, &gs.rules, "the server's rules", api.EndpointGuilds+id+"/member-verification?with_guild=false", func() memberVerification { return memberVerification{} })
	default:
		c.loadGuildList(p)
	}
}

func (c *chatView) closeGuildSettings() {
	c.gset.open = false
	c.closeMenu()
}

// patchGuild changes the server: apply shows the change at once, and body
// is what Discord is sent.
func (c *chatView) patchGuild(apply func(*discord.Guild), body map[string]any) {
	gs := &c.gset
	apply(&gs.guild)
	gs.err, gs.info = "", ""
	if c.demo {
		gs.info = "Saved."
		return
	}
	guildID := gs.guildID
	go func() {
		g, err := c.st.Client.RequestJSON[discord.Guild]("PATCH", api.EndpointGuilds+guildID.String(), httputil.WithJSONBody(body))
		c.u.post(func() {
			if gs.guildID != guildID {
				return
			}
			if err != nil {
				gs.err = "Discord did not take the change: " + err.Error()
				if old, err := c.st.Cabinet.Guild(guildID); err == nil {
					gs.guild = *old
				}
				return
			}
			gs.guild, gs.info = g, "Saved."
		})
	}()
}

// guildRequest sends Discord a change of the server other than its own
// fields, and calls done once it took it.
func (c *chatView) guildRequest(method, endpoint string, body any, done func()) {
	gs := &c.gset
	gs.err, gs.info = "", ""
	if c.demo {
		if done != nil {
			done()
		}
		return
	}
	guildID := gs.guildID
	go func() {
		var opts []httputil.RequestOption
		if body != nil {
			opts = append(opts, httputil.WithJSONBody(body))
		}
		err := c.st.Client.FastRequest(method, endpoint, opts...)
		c.u.post(func() {
			if gs.guildID != guildID {
				return
			}
			if err != nil {
				gs.err = "Discord did not take the change: " + err.Error()
				return
			}
			gs.info = "Saved."
			if done != nil {
				done()
			}
		})
	}()
}

// guildChannels are the server's channels of the kinds given, by position.
func (c *chatView) guildChannels(kinds ...discord.ChannelType) []discord.Channel {
	channels, _ := c.st.Cabinet.Channels(c.gset.guildID)
	var out []discord.Channel
	for _, ch := range channels {
		if slices.Contains(kinds, ch.Type) {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b discord.Channel) int { return a.Position - b.Position })
	return out
}

func (c *chatView) channelName(id discord.ChannelID, none string) string {
	if !id.IsValid() {
		return none
	}
	if ch, err := c.st.Cabinet.Channel(id); err == nil {
		return ch.Name
	}
	return none
}

// channelChoices are the server's channels of the kinds given as a menu,
// with none first, which pick calls back.
func (c *chatView) channelChoices(none string, chosen discord.ChannelID, pick func(discord.ChannelID), kinds ...discord.ChannelType) func() []menuItem {
	return func() []menuItem {
		items := []menuItem{{key: "none", text: none, check: checked(!chosen.IsValid()), act: func() { pick(0) }}}
		for _, ch := range c.guildChannels(kinds...) {
			ch := ch
			prefix := "# "
			if ch.Type == discord.GuildVoice || ch.Type == discord.GuildStageVoice {
				prefix = "🔊 "
			}
			items = append(items, menuItem{key: "ch" + ch.ID.String(), text: prefix + ch.Name, check: checked(chosen == ch.ID), act: func() { pick(ch.ID) }})
		}
		return items
	}
}

func (c *chatView) updateGuildSettings(gtx layout.Context) {
	gs := &c.gset
	if !gs.focused {
		gtx.Execute(key.FocusCmd{})
		gs.focused = true
	}
	for {
		e, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := e.(key.Event); ok && e.State == key.Press {
			c.closeGuildSettings()
			return
		}
	}
	if gs.closeBtn.Clicked(gtx) {
		c.closeGuildSettings()
		return
	}
	for p := range gsPages {
		if gs.pages[p].Clicked(gtx) && gs.page != p {
			c.showGuildPage(p)
		}
	}
	g := &gs.guild
	id := gs.guildID.String()

	// Server Profile.
	if gs.save.Clicked(gtx) && !gs.saving {
		name, desc := strings.TrimSpace(gs.name.Text()), strings.TrimSpace(gs.desc.Text())
		body := map[string]any{}
		if name != gs.origName && name != "" {
			body["name"] = name
		}
		if desc != gs.origDesc {
			body["description"] = desc
		}
		if len(body) > 0 {
			c.patchGuild(func(g *discord.Guild) { g.Name, g.Description = name, desc }, body)
			gs.origName, gs.origDesc = name, desc
		}
	}
	if gs.reset.Clicked(gtx) {
		gs.name.SetText(gs.origName)
		gs.desc.SetText(gs.origDesc)
	}
	if gs.iconBtn.Clicked(gtx) {
		c.pickGuildIcon()
	}
	if gs.iconRemove.Clicked(gtx) {
		c.patchGuild(func(g *discord.Guild) { g.Icon = "" }, map[string]any{"icon": nil})
	}
	for i := range gs.bannerBtns {
		if gs.bannerBtns[i].Clicked(gtx) {
			hex := fmt.Sprintf("#%06x", bannerPresets[i])
			gs.brand.data.BrandColor = hex
			c.guildRequest("PATCH", api.EndpointGuilds+id+"/profile", map[string]any{"brand_color_primary": hex}, nil)
		}
	}

	// Engagement: a flag set suppresses its kind of system message.
	sysBits := [4]discord.SystemChannelFlags{1 << 0, 1 << 3, 1 << 1, 1 << 2}
	for i, bit := range sysBits {
		if gs.sysFlags[i].Clicked(gtx) {
			flags := g.SystemChannelFlags ^ bit
			c.patchGuild(func(g *discord.Guild) { g.SystemChannelFlags = flags }, map[string]any{"system_channel_flags": flags})
		}
	}
	if gs.sysChan.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, c.channelChoices("No System Messages", g.SystemChannelID, func(ch discord.ChannelID) {
			c.patchGuild(func(g *discord.Guild) { g.SystemChannelID = ch }, map[string]any{"system_channel_id": nullID(discord.Snowflake(ch))})
		}, discord.GuildText))
	}
	for i := range gs.notify {
		if gs.notify[i].Clicked(gtx) {
			n := discord.Notification(i)
			c.patchGuild(func(g *discord.Guild) { g.Notification = n }, map[string]any{"default_message_notifications": i})
		}
	}
	if gs.afkChan.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, c.channelChoices("No Inactive Channel", g.AFKChannelID, func(ch discord.ChannelID) {
			c.patchGuild(func(g *discord.Guild) { g.AFKChannelID = ch }, map[string]any{"afk_channel_id": nullID(discord.Snowflake(ch))})
		}, discord.GuildVoice))
	}
	if gs.afkT.Clicked(gtx) {
		c.openMenu(c.pointerAt, 200, func() []menuItem {
			var items []menuItem
			for _, s := range []discord.Seconds{60, 300, 900, 1800, 3600} {
				s := s
				items = append(items, menuItem{key: fmt.Sprint("afk", s), text: afkName(s), check: checked(g.AFKTimeout == s), act: func() {
					c.patchGuild(func(g *discord.Guild) { g.AFKTimeout = s }, map[string]any{"afk_timeout": s})
				}})
			}
			return items
		})
	}
	if gs.widgetBtn.Clicked(gtx) && gs.widget.loaded {
		on := !gs.widget.data.Enabled
		gs.widget.data.Enabled = on
		c.guildRequest("PATCH", api.EndpointGuilds+id+"/widget", map[string]any{"enabled": on}, nil)
	}

	// Access.
	if gs.ageBtn.Clicked(gtx) {
		level := discord.NSFWLevel(3)
		if g.NSFWLevel == 3 {
			level = 0
		}
		c.patchGuild(func(g *discord.Guild) { g.NSFWLevel = level }, map[string]any{"nsfw_level": level})
	}
	rulesChanged := false
	addRule := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		r := &gs.rules.data
		if len(r.FormFields) == 0 {
			r.FormFields = append(r.FormFields, struct {
				FieldType string   `json:"field_type"`
				Label     string   `json:"label"`
				Values    []string `json:"values"`
				Required  bool     `json:"required"`
			}{FieldType: "TERMS", Label: "Read and agree to the server rules", Required: true})
		}
		r.FormFields[0].Values = append(r.FormFields[0].Values, text)
		rulesChanged = true
	}
	if gs.addRule.Clicked(gtx) {
		addRule(gs.rule.Text())
		gs.rule.SetText("")
	}
	for {
		e, ok := gs.rule.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			addRule(gs.rule.Text())
			gs.rule.SetText("")
		}
	}
	for i := range gs.exampleRules {
		if gs.exampleRules[i].Clicked(gtx) {
			addRule(exampleRules[i])
		}
	}
	for i, click := range gs.removeRule {
		if click.Clicked(gtx) && len(gs.rules.data.FormFields) > 0 && i < len(gs.rules.data.FormFields[0].Values) {
			f := &gs.rules.data.FormFields[0]
			f.Values = slices.Delete(f.Values, i, i+1)
			rulesChanged = true
		}
	}
	if gs.rulesBtn.Clicked(gtx) {
		on := !rulesOn(g)
		c.guildRequest("PATCH", api.EndpointGuilds+id+"/member-verification", map[string]any{"enabled": on, "form_fields": gs.rules.data.FormFields}, nil)
		if on {
			g.Features = append(g.Features, "MEMBER_VERIFICATION_GATE_ENABLED")
		} else {
			g.Features = slices.DeleteFunc(g.Features, func(f discord.GuildFeature) bool { return f == "MEMBER_VERIFICATION_GATE_ENABLED" })
		}
	}
	if rulesChanged {
		c.guildRequest("PATCH", api.EndpointGuilds+id+"/member-verification", map[string]any{"form_fields": gs.rules.data.FormFields}, nil)
	}

	// Safety Setup.
	if gs.verifBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 300, func() []menuItem {
			var items []menuItem
			for i, v := range verificationLevels {
				lvl := discord.Verification(i)
				items = append(items, menuItem{key: "v" + v.name, text: v.name, check: checked(g.Verification == lvl), act: func() {
					c.patchGuild(func(g *discord.Guild) { g.Verification = lvl }, map[string]any{"verification_level": lvl})
				}})
			}
			return items
		})
	}
	if gs.filterBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 300, func() []menuItem {
			var items []menuItem
			for i, f := range contentFilters {
				lvl := discord.ExplicitFilter(i)
				items = append(items, menuItem{key: "f" + f.name, text: f.name, check: checked(g.ExplicitFilter == lvl), act: func() {
					c.patchGuild(func(g *discord.Guild) { g.ExplicitFilter = lvl }, map[string]any{"explicit_content_filter": lvl})
				}})
			}
			return items
		})
	}
	if gs.mfaBtn.Clicked(gtx) {
		level := 1
		if g.MFA == 1 {
			level = 0
		}
		c.guildRequest("POST", api.EndpointGuilds+id+"/mfa", map[string]any{"level": level}, func() { g.MFA = discord.MFALevel(level) })
	}
	if gs.alertsBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, c.channelChoices("No channel", gs.safetyChannel, func(ch discord.ChannelID) {
			c.patchGuild(func(*discord.Guild) {}, map[string]any{"safety_alerts_channel_id": nullID(discord.Snowflake(ch))})
			gs.safetyChannel = ch
		}, discord.GuildText))
	}

	c.updateGuildLists(gtx)
}

// nullID is id, or null for none.
func nullID(id discord.Snowflake) any {
	if !id.IsValid() {
		return nil
	}
	return id.String()
}

func afkName(s discord.Seconds) string {
	if s >= 3600 {
		return "1 hour"
	}
	m := int(s) / 60
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

func rulesOn(g *discord.Guild) bool {
	return slices.Contains(g.Features, "MEMBER_VERIFICATION_GATE_ENABLED")
}

var exampleRules = [4]string{
	"Be civil and respectful",
	"No spam or self-promotion",
	"No age-restricted or obscene content",
	"Help keep things safe",
}

var verificationLevels = []struct{ name, about string }{
	{"None", "Unrestricted"},
	{"Low", "Must have a verified email on their Discord account."},
	{"Medium", "Must also be registered on Discord for longer than 5 minutes."},
	{"High", "Must also be a member of this server for longer than 10 minutes."},
	{"Highest", "Must have a verified phone on their Discord account."},
}

var contentFilters = []struct{ name, about string }{
	{"Do not filter", "No messages will be filtered for sensitive image media."},
	{"Filter messages from server members without roles", "Messages from members without roles will be filtered for sensitive image media."},
	{"Filter messages from all members", "All messages will be filtered for sensitive image media."},
}

// pickGuildIcon asks for a picture and makes it the server's icon.
func (c *chatView) pickGuildIcon() {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		path, err := zenity.SelectFile(zenity.Title("Server icon"), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"}})
		if err != nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 10<<20 {
			c.u.post(func() { c.gset.err = "Could not use that picture." })
			return
		}
		uri := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
		c.u.post(func() {
			c.patchGuild(func(*discord.Guild) {}, map[string]any{"icon": uri})
		})
	}()
}

// layoutGuildSettings draws the settings of the server, laid out as the
// user's settings.
func (c *chatView) layoutGuildSettings(gtx layout.Context) layout.Dimensions {
	c.updateGuildSettings(gtx)
	size := gtx.Constraints.Max
	if !c.gset.open {
		return layout.Dimensions{Size: size}
	}
	sideW := min(gtx.Dp(232)+max(size.X-gtx.Dp(232+40+740+72), 0)/2, size.X/2)
	paint.FillShape(gtx.Ops, colorSidebar, clip.Rect{Max: image.Pt(sideW, size.Y)}.Op())
	paint.FillShape(gtx.Ops, colorChat, clip.Rect{Min: image.Pt(sideW, 0), Max: size}.Op())

	listW := max(min(gtx.Dp(218), sideW-gtx.Dp(12)), 0)
	top := gtx.Dp(60)
	t := op.Offset(image.Pt(sideW-listW-gtx.Dp(6), top)).Push(gtx.Ops)
	sgtx := gtx
	sgtx.Constraints = layout.Constraints{Min: image.Pt(listW, 0), Max: image.Pt(listW, max(size.Y-top, 0))}
	c.layoutGuildSidebar(sgtx)
	t.Pop()

	x := sideW + gtx.Dp(40)
	w := max(min(gtx.Dp(740), size.X-x-gtx.Dp(72)), 0)
	t = op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
	pgtx := gtx
	pgtx.Constraints = layout.Exact(image.Pt(w, size.Y))
	c.layoutGuildPage(pgtx)
	t.Pop()

	t = op.Offset(image.Pt(x+w+gtx.Dp(16), top)).Push(gtx.Ops)
	closeCircle(gtx, c.u.th, &c.gset.closeBtn)
	t.Pop()
	return layout.Dimensions{Size: size}
}

func (c *chatView) layoutGuildSidebar(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	gs := &c.gset
	heading := func(txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Bottom: unit.Dp(6), Top: unit.Dp(10)}.Layout(gtx, oneLine(bold(label(th, 12, colorMuted, txt))).Layout)
		})
	}
	var children []layout.FlexChild
	for p := range gsPages {
		switch p {
		case gsProfile:
			children = append(children, heading(strings.ToUpper(gs.guild.Name)))
		case gsMembers:
			children = append(children, heading("PEOPLE"))
		case gsSafety:
			children = append(children, heading("MODERATION"))
		}
		p := p
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rowItem(gtx, &gs.pages[p], func(gtx layout.Context, hovered bool) layout.Dimensions {
					fg := colorMuted
					if hovered || gs.page == p {
						fg = colorHeader
					}
					return oneLine(label(th, 16, fg, gsPageNames[p])).Layout(gtx)
				}, gs.page == p)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// closeCircle is the round button that closes a settings screen, with ESC
// under it.
func closeCircle(gtx layout.Context, th *material.Theme, click *widget.Clickable) layout.Dimensions {
	d := gtx.Dp(36)
	click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		col := colorMuted
		if click.Hovered() {
			col = colorHeader
			paint.FillShape(gtx.Ops, colorHover, clip.Ellipse(image.Rect(0, 0, d, d)).Op(gtx.Ops))
		}
		sw := float32(max(gtx.Dp(2), 1))
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Ellipse(image.Rect(1, 1, d-1, d-1)).Path(gtx.Ops), Width: sw}.Op())
		gtx.Constraints = layout.Exact(image.Pt(d, d))
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconClose, 20, col) })
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
	t := op.Offset(image.Pt(gtx.Dp(6), d+gtx.Dp(6))).Push(gtx.Ops)
	bold(label(th, 12, colorMuted, "ESC")).Layout(gtx)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(d, d+gtx.Dp(26))}
}

func (c *chatView) layoutGuildPage(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	gs := &c.gset
	var rows []layout.Widget
	switch gs.page {
	case gsProfile:
		rows = c.guildProfileRows()
	case gsEngagement:
		rows = c.engagementRows()
	case gsAccess:
		rows = c.accessRows()
	case gsSafety:
		rows = c.safetyRows()
	default:
		rows = c.guildListRows()
	}
	head := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(60), Bottom: unit.Dp(16)}.Layout(gtx, bold(label(th, 20, colorHeader, gsPageNames[gs.page])).Layout)
	}}
	if gs.err != "" {
		head = append(head, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, gs.err).Layout)
		})
	} else if gs.info != "" {
		head = append(head, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorGreen, gs.info).Layout)
		})
	}
	rows = append(head, rows...)
	rows = append(rows, layout.Spacer{Height: unit.Dp(80)}.Layout)
	return listLayout(gtx, th, &gs.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, rows[i])
	})
}

// section is a part of a settings page with its title and what it is for.
func section(th *material.Theme, title, about string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(10)}.Layout(gtx, stacked(
			bold(label(th, 16, colorHeader, title)).Layout,
			func(gtx layout.Context) layout.Dimensions {
				if about == "" {
					return layout.Dimensions{}
				}
				return label(th, 14, colorMuted, about).Layout(gtx)
			},
		))
	}
}

func gap(h unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, h) }
}

// guildProfileRows are the Server Profile page: the name, icon, banner and
// description, beside a preview of how invites show the server.
func (c *chatView) guildProfileRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	form := []layout.Widget{
		settingsText(th, "Customize how your server appears in invite links and, if enabled, in Server Discovery and announcement messages."),
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		section(th, "Name", ""),
		func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &gs.name, "Server name") },
		gap(20),
		section(th, "Icon", "We recommend an image of at least 512x512."),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &gs.iconBtn, colorAccent, "Change Server Icon")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if gs.guild.Icon == "" {
						return layout.Dimensions{}
					}
					return button(gtx, th, &gs.iconRemove, colorSecondaryButton, "Remove Icon")
				}),
			)
		},
		gap(20),
		section(th, "Banner", ""),
		func(gtx layout.Context) layout.Dimensions {
			cols := 5
			gapPx := gtx.Dp(8)
			w := (gtx.Constraints.Max.X - (cols-1)*gapPx) / cols
			h := gtx.Dp(56)
			chosen := strings.ToLower(gs.brand.data.BrandColor)
			for i, col := range bannerPresets {
				x, y := (i%cols)*(w+gapPx), (i/cols)*(h+gapPx)
				t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
				bgtx := gtx
				bgtx.Constraints = layout.Exact(image.Pt(w, h))
				gs.bannerBtns[i].Layout(bgtx, func(gtx layout.Context) layout.Dimensions {
					r := image.Rect(0, 0, w, h)
					drawBannerGradient(gtx, r, rgb(col), gtx.Dp(6))
					if chosen == fmt.Sprintf("#%06x", col) || gs.bannerBtns[i].Hovered() {
						sw := float32(gtx.Dp(2))
						paint.FillShape(gtx.Ops, colorHeader, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(6)).Path(gtx.Ops), Width: sw}.Op())
					}
					return layout.Dimensions{Size: r.Max}
				})
				t.Pop()
			}
			rows := (len(bannerPresets) + cols - 1) / cols
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rows*h+(rows-1)*gapPx)}
		},
		gap(20),
		section(th, "Description", "How did your server get started? Why should people join?"),
		func(gtx layout.Context) layout.Dimensions {
			return textField(gtx, th, &gs.desc, "Tell people about your server")
		},
	}
	rows := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(620) {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(stacked(form...)), layout.Rigid(c.layoutGuildPreview))
		}
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, stacked(form...)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(280)
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return c.layoutGuildPreview(gtx)
			}),
		)
	}}
	changed := strings.TrimSpace(gs.name.Text()) != gs.origName || strings.TrimSpace(gs.desc.Text()) != gs.origDesc
	if changed {
		rows = append(rows, saveBar(th, true, gs.saving, "", "", &gs.reset, &gs.save))
	}
	return rows
}

// drawBannerGradient fills r with col fading to a lighter col, as Discord's
// banners.
func drawBannerGradient(gtx layout.Context, r image.Rectangle, col colorT, radius int) {
	light := colorT{R: paler(col.R), G: paler(col.G), B: paler(col.B), A: 0xff}
	st := clip.UniformRRect(r, radius).Push(gtx.Ops)
	paint.LinearGradientOp{Stop1: f32.Pt(0, float32(r.Min.Y)), Color1: col, Stop2: f32.Pt(0, float32(r.Max.Y)), Color2: light}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	st.Pop()
}

func paler(v uint8) uint8 { return uint8(int(v) + (255-int(v))*2/5) }

// layoutGuildPreview is the server as its invites show it.
func (c *chatView) layoutGuildPreview(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	gs := &c.gset
	g := gs.guild
	w := gtx.Constraints.Max.X
	brand := colorAccent
	if b := strings.TrimPrefix(gs.brand.data.BrandColor, "#"); len(b) == 6 {
		var v uint32
		fmt.Sscanf(b, "%06x", &v)
		brand = rgb(v)
	}
	name := strings.TrimSpace(gs.name.Text())
	desc := strings.TrimSpace(gs.desc.Text())
	online, members := g.ApproximatePresences, g.ApproximateMembers
	if members == 0 {
		if ms, err := c.st.Cabinet.Members(g.ID); err == nil {
			members = uint64(len(ms))
		}
	}
	return rounded(gtx, colorPopup, 10, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = w
		bannerH, iconPx := gtx.Dp(110), gtx.Dp(64)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				r := gtx.Dp(10)
				st := clip.RRect{Rect: image.Rect(0, 0, w, bannerH), NW: r, NE: r}.Push(gtx.Ops)
				paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: brand, Stop2: f32.Pt(0, float32(bannerH)), Color2: colorT{R: paler(brand.R), G: paler(brand.G), B: paler(brand.B), A: 0xff}}.Add(gtx.Ops)
				paint.PaintOp{}.Add(gtx.Ops)
				st.Pop()
				t := op.Offset(image.Pt(gtx.Dp(16), bannerH-iconPx/2)).Push(gtx.Ops)
				c.u.picture(gtx, cdnSize(g.IconURLWithType(discord.PNGImage)), unit.Dp(float32(iconPx)/gtx.Metric.PxPerDp), colorSidebar, name, gtx.Dp(16))
				t.Pop()
				return layout.Dimensions{Size: image.Pt(w, bannerH+iconPx/2)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					rows := []layout.FlexChild{
						layout.Rigid(bold(label(th, 18, colorHeader, name)).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dot(gtx, colorGreen, 8) }),
								layout.Rigid(label(th, 13, colorMuted, fmt.Sprintf(" %d Online   ", online)).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dot(gtx, colorMuted, 8) }),
								layout.Rigid(label(th, 13, colorMuted, fmt.Sprintf(" %d Members", members)).Layout),
							)
						}),
						layout.Rigid(label(th, 13, colorMuted, "Est. "+g.ID.Time().Format("Jan 2006")).Layout),
					}
					if desc != "" {
						rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout), layout.Rigid(label(th, 13, colorText, desc).Layout))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				})
			}),
		)
	})
}

// engagementRows are the Engagement page.
func (c *chatView) engagementRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	g := &gs.guild
	flag := func(i int, bit discord.SystemChannelFlags, text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return switchRow(gtx, th, &gs.sysFlags[i], text, "", g.SystemChannelFlags&bit == 0)
			})
		}
	}
	notify := []string{"All Messages", "Only @mentions"}
	rows := []layout.Widget{
		section(th, "System Messages", "Configure system event messages sent to your server."),
		flag(0, 1<<0, "Send a random welcome message when someone joins this server."),
		flag(1, 1<<3, "Prompt members to reply to welcome messages with a sticker."),
		flag(2, 1<<1, "Send a message when someone boosts this server."),
		flag(3, 1<<2, "Send helpful tips for server setup."),
		twoColumns(section(th, "System Messages Channel", "This is the channel we send system event messages to."), func(gtx layout.Context) layout.Dimensions {
			return dropdown(gtx, th, &gs.sysChan, c.channelName(g.SystemChannelID, "No System Messages"))
		}),
		gap(20),
		section(th, "Default Notification Settings", "Whether members who haven't explicitly set their notification settings receive a notification for every message sent in this server."),
	}
	for i, n := range notify {
		i, n := i, n
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &gs.notify[i], n, int(g.Notification) == i)
		})
	}
	rows = append(rows,
		gap(20),
		section(th, "Inactive Channel", "Automatically move members to this channel and mute them when they have been idle for longer than the inactive timeout."),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return dropdown(gtx, th, &gs.afkChan, c.channelName(g.AFKChannelID, "No Inactive Channel"))
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return dropdown(gtx, th, &gs.afkT, afkName(max(g.AFKTimeout, 60)))
				}),
			)
		},
		gap(20),
		section(th, "Server Widget", "Embed an HTML widget on your website to show your online members, voice channels and invite link."),
		func(gtx layout.Context) layout.Dimensions {
			if !gs.widget.loaded {
				return label(th, 14, colorMuted, "Loading…").Layout(gtx)
			}
			return switchRow(gtx, th, &gs.widgetBtn, "Enable Server Widget", "", gs.widget.data.Enabled)
		},
	)
	return rows
}

// accessRows are the Access page.
func (c *chatView) accessRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	g := &gs.guild
	mode := 0
	if slices.Contains(g.Features, "DISCOVERABLE") {
		mode = 2
	}
	card := func(i int, title, about string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			bg := colorSidebar
			fg := colorMuted
			if i == mode {
				bg, fg = colorSelected, colorHeader
			}
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(12)).Layout(gtx, stacked(
						bold(label(th, 15, fg, title)).Layout,
						label(th, 12, colorMuted, about).Layout,
					))
				})
			})
		})
	}
	rows := []layout.Widget{
		section(th, "How can people join your server?", "Keep your server private, or open it to more people. Changing this is done in Discord's own app."),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				card(0, "Invite Only", "People can join your server directly with an invite."),
				card(1, "Apply to Join", "People must submit an application and be approved."),
				card(2, "Discoverable", "Anyone can join your server directly through Server Discovery."),
			)
		},
		gap(20),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &gs.ageBtn, "Age-Restricted Server", "Users will need to confirm they are of legal age to view the content in this server.", g.NSFWLevel == 3)
		},
		gap(20),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &gs.rulesBtn, "Server Rules", "Members must agree to the rules before they can talk or interact in the server.", rulesOn(g))
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	if !gs.rules.loaded {
		return append(rows, gs.rules.note(th)...)
	}
	var values []string
	if len(gs.rules.data.FormFields) > 0 {
		values = gs.rules.data.FormFields[0].Values
	}
	if gs.removeRule == nil {
		gs.removeRule = map[int]*widget.Clickable{}
	}
	rows = append(rows, settingsHeading(th, "RULES"))
	for i, v := range values {
		i, v := i, v
		click := clickFor(&gs.removeRule, i)
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, label(th, 14, colorText, fmt.Sprintf("%d. %s", i+1, v)).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, click, iconClose, colorMuted, colorRed)
				}),
			)
		}))
	}
	rows = append(rows,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &gs.rule, "Enter a rule") }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, &gs.addRule, colorAccent, "Add a Rule")
				}),
			)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		settingsHeading(th, "EXAMPLE RULES"),
		func(gtx layout.Context) layout.Dimensions {
			var chips []layout.FlexChild
			for i, r := range exampleRules {
				i, r := i, r
				chips = append(chips, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &gs.exampleRules[i], colorSecondaryButton, r)
					})
				}))
			}
			return wrapLayout(gtx, chips)
		},
	)
	return rows
}

// safetyRows are the Safety Setup page.
func (c *chatView) safetyRows() []layout.Widget {
	th := c.u.th
	gs := &c.gset
	g := &gs.guild
	v := verificationLevels[min(int(g.Verification), len(verificationLevels)-1)]
	f := contentFilters[min(int(g.ExplicitFilter), len(contentFilters)-1)]
	owner := g.OwnerID == c.me.ID
	choice := func(click *widget.Clickable, title, about string) layout.Widget {
		return settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, stacked(bold(label(th, 15, colorHeader, title)).Layout, label(th, 13, colorMuted, about).Layout)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, click, colorSecondaryButton, "Change")
				}),
			)
		})
	}
	rows := []layout.Widget{
		section(th, "Verification Level", "Members of the server must meet the following criteria before they can send messages in text channels or start a direct message conversation."),
		choice(&gs.verifBtn, v.name, v.about),
		gap(20),
		func(gtx layout.Context) layout.Dimensions {
			about := "Moderators must have two-factor authentication enabled to delete messages, ban, kick or time out members."
			if !owner {
				about += " Only the server owner can change this."
			}
			return switchRow(gtx, th, &gs.mfaBtn, "Require 2FA for moderator actions", about, g.MFA == 1)
		},
		gap(20),
		section(th, "Sensitive Content Filters", "Choose if server members can share image-based media detected by Discord's sensitive media filters."),
		choice(&gs.filterBtn, f.name, f.about),
		gap(20),
		section(th, "Safety Notifications Channel", "Anyone with access to this text channel will be able to see the safety notifications."),
		func(gtx layout.Context) layout.Dimensions {
			return dropdown(gtx, th, &gs.alertsBtn, c.channelName(gs.safetyChannel, "Select…"))
		},
	}
	return rows
}

// loadSafetyChannel is the safety channel Discord gives with the server.
func (c *chatView) loadSafetyChannel() {
	gs := &c.gset
	if c.demo {
		return
	}
	guildID := gs.guildID
	go func() {
		g, err := c.st.Client.RequestJSON[struct {
			SafetyAlerts discord.ChannelID `json:"safety_alerts_channel_id"`
		}]("GET", api.EndpointGuilds+guildID.String())
		if err != nil {
			slog.Warn("could not load the server", "err", err)
			return
		}
		c.u.post(func() {
			if gs.guildID == guildID {
				gs.safetyChannel = g.SafetyAlerts
			}
		})
	}()
}
