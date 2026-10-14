package gui

import (
	"fmt"
	"image"
	"net/url"
	"strings"
	"unicode/utf8"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// serverFields are what the user shows of themselves in one server only.
type serverFields struct {
	nick, pronouns, bio string
}

// serverProfileView edits the user's profile in one server, as the Server
// Profiles tab of Discord's Profiles page.
type serverProfileView struct {
	guildID             discord.GuildID
	nick, pronouns, bio widget.Editor
	orig                serverFields
	loading             bool
	pick, save, reset   widget.Clickable
	saving              bool
	saveErr, saveInfo   string
}

func (sp *serverProfileView) fields() serverFields {
	return serverFields{
		nick:     strings.TrimSpace(sp.nick.Text()),
		pronouns: strings.TrimSpace(sp.pronouns.Text()),
		bio:      strings.TrimSpace(sp.bio.Text()),
	}
}

func (sp *serverProfileView) setFields(f serverFields) {
	sp.orig = f
	sp.nick.SetText(f.nick)
	sp.pronouns.SetText(f.pronouns)
	sp.bio.SetText(f.bio)
	sp.nick.SingleLine, sp.pronouns.SingleLine = true, true
	sp.nick.MaxLen = 32
}

// openServerProfile opens the settings on the user's profile in the server.
func (c *chatView) openServerProfile(guildID discord.GuildID) {
	c.openSettings()
	c.showSettingsPage(pageProfiles)
	c.settings.acct.tab = 1
	c.loadServerProfile(guildID)
}

// loadServerProfile starts editing the user's profile in the server, with
// what is known of it, and asks Discord for the rest.
func (c *chatView) loadServerProfile(guildID discord.GuildID) {
	sp := &c.settings.acct.server
	sp.guildID, sp.saveErr, sp.saveInfo = guildID, "", ""
	var f serverFields
	if m, err := c.st.Cabinet.Member(guildID, c.me.ID); err == nil {
		f.nick = m.Nick
	}
	sp.setFields(f)
	if c.demo || !guildID.IsValid() {
		return
	}
	sp.loading = true
	me := c.me.ID
	go func() {
		q := url.Values{"guild_id": {guildID.String()}, "with_mutual_guilds": {"false"}, "with_mutual_friends": {"false"}}
		p, err := c.st.Client.RequestJSON[*profileData]("GET", api.EndpointUsers+me.String()+"/profile?"+q.Encode())
		c.u.post(func() {
			if sp.guildID != guildID {
				return
			}
			sp.loading = false
			if err != nil || p == nil || p.GuildMemberProfile == nil {
				return
			}
			// What was typed meanwhile is kept.
			if sp.fields() == sp.orig {
				f := sp.orig
				f.bio, f.pronouns = p.GuildMemberProfile.Bio, p.GuildMemberProfile.Pronouns
				sp.setFields(f)
			}
		})
	}()
}

// saveServerProfile sends what changed of the server profile to Discord.
func (c *chatView) saveServerProfile() {
	sp := &c.settings.acct.server
	f, o := sp.fields(), sp.orig
	if utf8.RuneCountInString(f.bio) > 190 {
		sp.saveErr = "About Me is 190 characters at most."
		return
	}
	body := map[string]any{}
	if f.nick != o.nick {
		body["nick"] = f.nick
	}
	if f.pronouns != o.pronouns {
		body["pronouns"] = f.pronouns
	}
	if f.bio != o.bio {
		body["bio"] = f.bio
	}
	sp.saving, sp.saveErr, sp.saveInfo = true, "", ""
	if c.demo {
		sp.saving, sp.orig, sp.saveInfo = false, f, "Saved."
		return
	}
	guildID := sp.guildID
	go func() {
		err := c.st.Client.FastRequest("PATCH", api.EndpointGuilds+guildID.String()+"/members/@me", httputil.WithJSONBody(body))
		c.u.post(func() {
			sp.saving = false
			if err != nil {
				sp.saveErr = "Not saved: " + err.Error()
				if strings.Contains(err.Error(), "50035") || strings.Contains(err.Error(), "premium") {
					sp.saveErr += " (server pronouns and About Me need Nitro)"
				}
				return
			}
			delete(c.profiles, profileKey{c.me.ID, guildID})
			if sp.guildID == guildID {
				sp.orig, sp.saveInfo = f, "Saved."
			}
		})
	}()
}

func (c *chatView) updateServerProfile(gtx layout.Context) {
	a := &c.settings.acct
	sp := &a.server
	for i := range a.tabs {
		if a.tabs[i].Clicked(gtx) {
			a.tab = i
			if i == 1 && !sp.guildID.IsValid() {
				gid := c.selGuild
				if !gid.IsValid() && len(c.guildOrder) > 0 {
					gid = c.guildOrder[0]
				}
				c.loadServerProfile(gid)
			}
		}
	}
	if sp.pick.Clicked(gtx) {
		c.openMenu(c.pointerAt, 260, c.serverPickItems)
	}
	if sp.save.Clicked(gtx) && !sp.saving {
		c.saveServerProfile()
	}
	if sp.reset.Clicked(gtx) {
		sp.setFields(sp.orig)
		sp.saveErr, sp.saveInfo = "", ""
	}
}

// serverPickItems are the servers whose profile can be edited.
func (c *chatView) serverPickItems() []menuItem {
	var items []menuItem
	for _, gid := range c.guildOrder {
		g, err := c.st.Cabinet.Guild(gid)
		if err != nil {
			continue
		}
		gid, icon, name := gid, cdnSize(g.IconURLWithType(discord.PNGImage)), g.Name
		items = append(items, menuItem{key: "pick" + gid.String(), text: name, lead: func(gtx layout.Context) layout.Dimensions {
			return c.u.picture(gtx, icon, 20, colorSidebar, name, -1)
		}, act: func() { c.loadServerProfile(gid) }})
	}
	return items
}

// layoutProfileTabs are the tabs of the Profiles page.
func (c *chatView) layoutProfileTabs(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	a := &c.settings.acct
	tab := func(i int, txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.tabs[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					col := colorMuted
					if a.tab == i || a.tabs[i].Hovered() {
						col = colorHeader
					}
					dims := layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, label(th, 16, col, txt).Layout)
					if a.tab == i {
						paint.FillShape(gtx.Ops, colorAccent, clip.Rect{Min: image.Pt(0, dims.Size.Y-gtx.Dp(2)), Max: dims.Size}.Op())
					}
					return dims
				})
			})
		})
	}
	return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx, tab(0, "User Profile"), tab(1, "Server Profiles"))
	})
}

// serverProfileRows are the Server Profiles tab: the server chosen, the
// fields and a preview.
func (c *chatView) serverProfileRows() []layout.Widget {
	th := c.u.th
	sp := &c.settings.acct.server
	f := sp.fields()
	name, gicon := "Choose a server", ""
	if g, err := c.st.Cabinet.Guild(sp.guildID); err == nil {
		name, gicon = g.Name, cdnSize(g.IconURLWithType(discord.PNGImage))
	}
	input := func(title string, ed *widget.Editor, hint string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, stacked(
				settingsHeading(th, title),
				func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, ed, hint) },
			))
		}
	}
	form := []layout.Widget{
		settingsText(th, "Show who you are with a different profile in each server. Server pronouns and About Me need Nitro."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		settingsHeading(th, "CHOOSE A SERVER"),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sp.pick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return rounded(gtx, colorInput, 6, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if !sp.guildID.IsValid() {
										return layout.Dimensions{}
									}
									return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return c.u.picture(gtx, gicon, 24, colorSidebar, name, -1)
									})
								}),
								layout.Flexed(1, oneLine(label(th, 15, colorHeader, name)).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMenuDown, 20, colorText) }),
							)
						})
					})
				})
			})
		},
	}
	if sp.guildID.IsValid() {
		form = append(form,
			input("SERVER NICKNAME", &sp.nick, c.me.DisplayOrUsername()),
			input("PRONOUNS", &sp.pronouns, "Add your pronouns"),
			func(gtx layout.Context) layout.Dimensions {
				n := utf8.RuneCountInString(f.bio)
				col := colorMuted
				if n > 190 {
					col = colorRed
				}
				return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, stacked(
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Flexed(1, settingsHeading(th, "ABOUT ME")),
							layout.Rigid(label(th, 12, col, fmt.Sprintf("%d/190", n)).Layout),
						)
					},
					func(gtx layout.Context) layout.Dimensions {
						return textField(gtx, th, &sp.bio, "Tell this server about you")
					},
				))
			},
		)
	}
	if sp.loading {
		form = append(form, settingsText(th, "Loading…"))
	}
	// The preview shows the server's profile over the user's: what is not
	// set for the server is the user's own.
	acct := c.settings.acct.fields()
	pf := profileFields{name: acct.name, username: c.me.Username, pronouns: acct.pronouns, bio: acct.bio, status: acct.status, emoji: acct.emoji}
	if f.nick != "" {
		pf.name = f.nick
	}
	if f.pronouns != "" {
		pf.pronouns = f.pronouns
	}
	if f.bio != "" {
		pf.bio = f.bio
	}
	rows := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		preview := func(gtx layout.Context) layout.Dimensions {
			if !sp.guildID.IsValid() {
				return layout.Dimensions{}
			}
			return c.layoutProfilePreview(gtx, pf, sp.guildID)
		}
		if gtx.Constraints.Max.X < gtx.Dp(620) {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(stacked(form...)), layout.Rigid(preview))
		}
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, stacked(form...)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(300)
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return preview(gtx)
			}),
		)
	}}
	if f != sp.orig || sp.saveErr != "" || sp.saveInfo != "" {
		rows = append(rows, saveBar(th, f != sp.orig, sp.saving, sp.saveErr, sp.saveInfo, &sp.reset, &sp.save))
	}
	return rows
}
