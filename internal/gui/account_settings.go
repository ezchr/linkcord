package gui

import (
	"fmt"
	"image"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"

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
)

// accountSettings are the settings of the account that Discord's settings
// endpoint gives and takes; others are only in its newer binary format.
type accountSettings struct {
	ExplicitContentFilter   int  `json:"explicit_content_filter"`
	DefaultGuildsRestricted bool `json:"default_guilds_restricted"`
	ShowCurrentGame         bool `json:"show_current_game"`
	// RestrictedGuilds are the servers whose members may not message the
	// user.
	RestrictedGuilds  []discord.GuildID `json:"restricted_guilds"`
	FriendSourceFlags struct {
		All           bool `json:"all,omitempty"`
		MutualFriends bool `json:"mutual_friends,omitempty"`
		MutualGuilds  bool `json:"mutual_guilds,omitempty"`
	} `json:"friend_source_flags"`
	CustomStatus *struct {
		Text      string `json:"text"`
		EmojiName string `json:"emoji_name"`
	} `json:"custom_status"`
}

// profileFields are what the Profiles page edits.
type profileFields struct {
	name, username, pronouns, bio, status, emoji string
}

// accountView is the state of the account's pages of the settings.
type accountView struct {
	loading, loaded bool
	err             string
	settings        accountSettings
	profile         *profileData

	// orig are the fields as Discord has them, which the editors start from.
	orig                                                   profileFields
	name, username, pronouns, bio, status, emoji, password widget.Editor
	save, reset, clearStatus, editProfile                  widget.Clickable
	saving                                                 bool
	saveErr, saveInfo                                      string

	filter                                [3]widget.Clickable
	dms, frAll, frFriends, frGuilds, game widget.Clickable

	// tab is the Profiles page's tab: the user's profile, or their profile
	// in one server.
	tab    int
	tabs   [2]widget.Clickable
	server serverProfileView
}

// loadAccount asks Discord for the account's settings and profile.
func (c *chatView) loadAccount() {
	a := &c.settings.acct
	if a.loading || c.demo {
		if c.demo && !a.loaded {
			a.loaded = true
			a.settings.ShowCurrentGame, a.settings.FriendSourceFlags.All = true, true
			a.setFields(profileFields{name: c.me.DisplayName, username: c.me.Username, bio: "This is the demo."})
		}
		return
	}
	a.loading = true
	me := c.me
	go func() {
		settings, err := c.st.Client.RequestJSON[accountSettings]("GET", api.EndpointMe+"/settings")
		q := url.Values{"with_mutual_guilds": {"false"}, "with_mutual_friends": {"false"}}
		profile, perr := c.st.Client.RequestJSON[*profileData]("GET", api.EndpointUsers+me.ID.String()+"/profile?"+q.Encode())
		c.u.post(func() {
			a.loading = false
			if err != nil {
				a.err = "Discord did not give the account's settings: " + err.Error()
				slog.Warn("could not load the account's settings", "err", err)
			} else {
				a.settings, a.loaded = settings, true
			}
			f := profileFields{name: c.me.DisplayName, username: c.me.Username}
			if perr == nil && profile != nil {
				a.profile = profile
				f.bio, f.pronouns = profile.UserProfile.Bio, profile.UserProfile.Pronouns
			}
			if cs := a.settings.CustomStatus; cs != nil {
				f.status, f.emoji = cs.Text, cs.EmojiName
			}
			a.setFields(f)
		})
	}()
}

// setFields has the editors start from f, as Discord has them.
func (a *accountView) setFields(f profileFields) {
	a.orig = f
	a.name.SetText(f.name)
	a.username.SetText(f.username)
	a.pronouns.SetText(f.pronouns)
	a.bio.SetText(f.bio)
	a.status.SetText(f.status)
	a.emoji.SetText(f.emoji)
	a.password.SetText("")
	for _, e := range []*widget.Editor{&a.name, &a.username, &a.pronouns, &a.status, &a.emoji, &a.password} {
		e.SingleLine = true
	}
	a.password.Mask = '•'
}

// fields are what the editors say.
func (a *accountView) fields() profileFields {
	return profileFields{
		name: strings.TrimSpace(a.name.Text()), username: strings.TrimSpace(a.username.Text()),
		pronouns: strings.TrimSpace(a.pronouns.Text()), bio: strings.TrimSpace(a.bio.Text()),
		status: strings.TrimSpace(a.status.Text()), emoji: strings.TrimSpace(a.emoji.Text()),
	}
}

// patchSettings changes the account's settings to what body says.
func (c *chatView) patchSettings(body map[string]any) {
	a := &c.settings.acct
	if c.demo {
		return
	}
	go func() {
		settings, err := c.st.Client.RequestJSON[accountSettings]("PATCH", api.EndpointMe+"/settings", httputil.WithJSONBody(body))
		c.u.post(func() {
			if err != nil {
				a.err = "Discord did not take the change: " + err.Error()
				return
			}
			a.err = ""
			a.settings = settings
		})
	}()
}

// saveProfile sends the fields changed to Discord.
func (c *chatView) saveProfile() {
	a := &c.settings.acct
	f, o := a.fields(), a.orig
	password := a.password.Text()
	if f.username != o.username && password == "" {
		a.saveErr = "Discord asks for your password to change your username."
		return
	}
	if utf8.RuneCountInString(f.bio) > 190 {
		a.saveErr = "About Me is 190 characters at most."
		return
	}
	a.saving, a.saveErr, a.saveInfo = true, "", ""
	if c.demo {
		a.saving, a.orig, a.saveInfo = false, f, "Saved."
		return
	}
	go func() {
		var errs []string
		user := map[string]any{}
		if f.name != o.name {
			if f.name == "" {
				user["global_name"] = nil
			} else {
				user["global_name"] = f.name
			}
		}
		if f.username != o.username {
			user["username"], user["password"] = f.username, password
		}
		var me *discord.User
		if len(user) > 0 {
			u, err := c.st.Client.RequestJSON[discord.User]("PATCH", api.EndpointMe, httputil.WithJSONBody(user))
			if err != nil {
				errs = append(errs, "name: "+err.Error())
			} else {
				me = &u
			}
		}
		if f.bio != o.bio || f.pronouns != o.pronouns {
			if err := c.st.Client.FastRequest("PATCH", api.EndpointMe+"/profile", httputil.WithJSONBody(map[string]any{"bio": f.bio, "pronouns": f.pronouns})); err != nil {
				errs = append(errs, "profile: "+err.Error())
			}
		}
		if f.status != o.status || f.emoji != o.emoji {
			var cs any
			if f.status != "" || f.emoji != "" {
				cs = map[string]any{"text": f.status, "emoji_name": f.emoji}
			}
			if err := c.st.Client.FastRequest("PATCH", api.EndpointMe+"/settings", httputil.WithJSONBody(map[string]any{"custom_status": cs})); err != nil {
				errs = append(errs, "status: "+err.Error())
			}
		}
		c.u.post(func() {
			a.saving = false
			if me != nil {
				c.me.DisplayName, c.me.Username = me.DisplayName, me.Username
			}
			if len(errs) > 0 {
				a.saveErr = "Some changes were not saved: " + strings.Join(errs, "; ")
				return
			}
			// The own profile is asked for again, as changed.
			delete(c.profiles, profileKey{c.me.ID, 0})
			if f.status != o.status || f.emoji != o.emoji {
				c.setMyCustomStatus(f.status, f.emoji)
			}
			a.orig = f
			a.password.SetText("")
			a.saveInfo = "Saved."
		})
	}()
}

// setMyCustomStatus shows the user's custom status at once.
func (c *chatView) setMyCustomStatus(text, emoji string) {
	var acts []discord.Activity
	if p, _ := c.st.PresenceStore.Presence(0, c.me.ID); p != nil {
		for _, a := range p.Activities {
			if a.Type != discord.CustomActivity {
				acts = append(acts, a)
			}
		}
	}
	if text != "" || emoji != "" {
		a := discord.Activity{Name: "Custom Status", Type: discord.CustomActivity, State: text}
		if emoji != "" {
			a.Emoji = &discord.Emoji{Name: emoji}
		}
		acts = append(acts, a)
	}
	if acts == nil {
		acts = []discord.Activity{}
	}
	c.setMyPresence(c.st.Status(), acts)
}

func (c *chatView) updateAccount(gtx layout.Context) {
	s := &c.settings
	a := &s.acct
	if a.editProfile.Clicked(gtx) {
		c.showSettingsPage(pageProfiles)
	}
	if a.save.Clicked(gtx) && !a.saving {
		c.saveProfile()
	}
	if a.reset.Clicked(gtx) {
		a.setFields(a.orig)
		a.saveErr, a.saveInfo = "", ""
	}
	c.updateServerProfile(gtx)
	if a.clearStatus.Clicked(gtx) {
		a.status.SetText("")
		a.emoji.SetText("")
	}
	for i, v := range []int{2, 1, 0} {
		if a.filter[i].Clicked(gtx) && a.settings.ExplicitContentFilter != v {
			a.settings.ExplicitContentFilter = v
			c.patchSettings(map[string]any{"explicit_content_filter": v})
		}
	}
	if a.dms.Clicked(gtx) {
		a.settings.DefaultGuildsRestricted = !a.settings.DefaultGuildsRestricted
		c.patchSettings(map[string]any{"default_guilds_restricted": a.settings.DefaultGuildsRestricted})
	}
	if a.game.Clicked(gtx) {
		a.settings.ShowCurrentGame = !a.settings.ShowCurrentGame
		c.patchSettings(map[string]any{"show_current_game": a.settings.ShowCurrentGame})
	}
	fs := &a.settings.FriendSourceFlags
	changed := false
	switch {
	case a.frAll.Clicked(gtx):
		fs.All, changed = !fs.All, true
		if fs.All {
			fs.MutualFriends, fs.MutualGuilds = true, true
		}
	case a.frFriends.Clicked(gtx):
		fs.MutualFriends, fs.All, changed = !(fs.All || fs.MutualFriends), false, true
	case a.frGuilds.Clicked(gtx):
		fs.MutualGuilds, fs.All, changed = !(fs.All || fs.MutualGuilds), false, true
	}
	if changed {
		c.patchSettings(map[string]any{"friend_source_flags": map[string]bool{
			"all": fs.All, "mutual_friends": fs.All || fs.MutualFriends, "mutual_guilds": fs.All || fs.MutualGuilds,
		}})
	}
}

// accountNote is what the account's pages say while loading or failing.
func (c *chatView) accountNote() []layout.Widget {
	a := &c.settings.acct
	th := c.u.th
	switch {
	case a.err != "":
		return []layout.Widget{func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, a.err).Layout)
		}}
	case a.loading:
		return []layout.Widget{settingsText(th, "Loading…")}
	}
	return nil
}

// myAccountRows are the My Account page: who the user is, and a way to
// edit their profile.
func (c *chatView) myAccountRows() []layout.Widget {
	th := c.u.th
	a := &c.settings.acct
	me := c.me
	email := me.Email
	if at := strings.IndexByte(email, '@'); at > 0 {
		email = strings.Repeat("*", min(at, 10)) + email[at:]
	}
	if email == "" {
		email = "Not shown"
	}
	twoFA := "Off"
	if me.MFA {
		twoFA = "On"
	}
	field := func(title, value string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, stacked(
				bold(label(th, 12, colorMuted, title)).Layout,
				label(th, 15, colorHeader, value).Layout,
			))
		}
	}
	name := me.DisplayName
	if name == "" {
		name = me.Username
	}
	return append(c.accountNote(),
		func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return c.avatarStatus(gtx, me, discord.NullGuildID, 64, colorSidebar)
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
								layout.Flexed(1, bold(label(th, 20, colorHeader, name)).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return button(gtx, th, &a.editProfile, colorAccent, "Edit User Profile")
								}),
							)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return rounded(gtx, colorServers, 8, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.UniformInset(unit.Dp(16)).Layout(gtx, stacked(
									field("DISPLAY NAME", name),
									field("USERNAME", me.Username),
									field("EMAIL", email),
									field("TWO-FACTOR AUTHENTICATION", twoFA),
								))
							})
						}),
					)
				})
			})
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		settingsText(th, "To change your email, password or two-factor authentication, use Discord's own app or website."),
	)
}

// profileRows are the Profiles page: the fields of the profile, and a
// preview of it as others see it.
func (c *chatView) profileRows() []layout.Widget {
	th := c.u.th
	a := &c.settings.acct
	if a.tab == 1 {
		return append([]layout.Widget{c.layoutProfileTabs}, c.serverProfileRows()...)
	}
	f := a.fields()
	input := func(title string, ed *widget.Editor, hint string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, stacked(
				settingsHeading(th, title),
				func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, ed, hint) },
			))
		}
	}
	form := []layout.Widget{
		input("DISPLAY NAME", &a.name, "How others see you"),
		input("USERNAME", &a.username, "username"),
	}
	if f.username != a.orig.username {
		form = append(form, input("CURRENT PASSWORD", &a.password, "Discord asks for it to change your username"))
	}
	form = append(form,
		input("PRONOUNS", &a.pronouns, "Add your pronouns"),
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
				func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &a.bio, "Tell others about you") },
			))
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, stacked(
				settingsHeading(th, "CUSTOM STATUS"),
				settingsText(th, "What you are thinking, shown in a bubble over your avatar."),
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Max.X = gtx.Dp(64)
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return textField(gtx, th, &a.emoji, "😀")
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return textField(gtx, th, &a.status, "What's on your mind?")
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return iconButton(gtx, &a.clearStatus, iconClose, colorMuted, colorRed)
							})
						}),
					)
				},
			))
		},
	)
	rows := append([]layout.Widget{c.layoutProfileTabs}, c.accountNote()...)
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		wide := gtx.Constraints.Max.X >= gtx.Dp(620)
		if !wide {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(stacked(form...)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.layoutProfilePreview(gtx, f, discord.NullGuildID) }),
			)
		}
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, stacked(form...)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(300)
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return c.layoutProfilePreview(gtx, f, discord.NullGuildID)
			}),
		)
	})
	// The bar that saves, while something changed, as in Discord.
	if f != a.orig || a.saveErr != "" || a.saveInfo != "" {
		rows = append(rows, saveBar(th, f != a.orig, a.saving, a.saveErr, a.saveInfo, &a.reset, &a.save))
	}
	return rows
}

// saveBar is the bar that saves what changed, or says how saving went, as
// Discord's.
func saveBar(th *material.Theme, changed, saving bool, saveErr, saveInfo string, reset, save *widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorServers, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					msg, col := "Careful — you have unsaved changes!", colorHeader
					switch {
					case saveErr != "":
						msg, col = saveErr, colorRed
					case saving:
						msg = "Saving…"
					case !changed && saveInfo != "":
						msg, col = saveInfo, colorGreen
					}
					children := []layout.FlexChild{layout.Flexed(1, label(th, 14, col, msg).Layout)}
					if changed {
						children = append(children,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, reset, colorSecondaryButton, "Reset")
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, save, colorGreenButton, "Save Changes")
							}),
						)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
				})
			})
		})
	}
}

// layoutProfilePreview is the profile as others will see it, with the
// fields as they are being edited, in the server guildID if valid.
func (c *chatView) layoutProfilePreview(gtx layout.Context, f profileFields, guildID discord.GuildID) layout.Dimensions {
	th := c.u.th
	a := &c.settings.acct
	me := c.me
	w := gtx.Constraints.Max.X
	banner := colorAccent
	bannerURL := ""
	if p := a.profile; p != nil {
		if p.UserProfile.AccentColor != nil {
			banner = rgb(uint32(*p.UserProfile.AccentColor))
		}
		if h := p.UserProfile.Banner; h != "" {
			ext := ".png"
			if strings.HasPrefix(h, "a_") {
				ext = ".gif"
			}
			bannerURL = "https://cdn.discordapp.com/banners/" + me.ID.String() + "/" + h + ext + "?size=600"
		}
	}
	name := f.name
	if name == "" {
		name = f.username
	}
	bubble := strings.TrimSpace(f.emoji + " " + f.status)
	return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, colorPopup, 8, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = w
			bannerH, avatar, ringPx := gtx.Dp(100), gtx.Dp(80), gtx.Dp(6)
			head := func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(w, bannerH+avatar/2+ringPx)
				r := gtx.Dp(8)
				top := clip.RRect{Rect: image.Rect(0, 0, w, bannerH), NE: r, NW: r}
				paint.FillShape(gtx.Ops, banner, top.Op(gtx.Ops))
				if img := c.u.images.frame(gtx, bannerURL, w); img != nil {
					st := top.Push(gtx.Ops)
					bgtx := gtx
					bgtx.Constraints = layout.Exact(image.Pt(w, bannerH))
					widget.Image{Src: *img, Fit: widget.Cover}.Layout(bgtx)
					st.Pop()
				}
				x, y := gtx.Dp(16), bannerH-avatar/2-ringPx
				paint.FillShape(gtx.Ops, colorPopup, clip.Ellipse(image.Rect(x, y, x+avatar+2*ringPx, y+avatar+2*ringPx)).Op(gtx.Ops))
				t := op.Offset(image.Pt(x+ringPx, y+ringPx)).Push(gtx.Ops)
				avSize := unit.Dp(float32(avatar) / gtx.Metric.PxPerDp)
				if guildID.IsValid() {
					// The server's own avatar, if the user has one there.
					dims := c.u.picture(gtx, c.authorAvatarURL(guildID, me), avSize, colorFor(uint64(me.ID)), name, -1)
					statusDot(gtx, c.status(guildID, me.ID), dims.Size.X, colorPopup)
				} else {
					c.avatarStatus(gtx, me, discord.NullGuildID, avSize, colorPopup)
				}
				t.Pop()
				// The custom status, in a thought bubble beside the avatar.
				if bubble != "" {
					bx := x + avatar + 2*ringPx + gtx.Dp(4)
					bgtx := gtx
					bgtx.Constraints = layout.Constraints{Max: image.Pt(w-bx-gtx.Dp(12), gtx.Dp(60))}
					m := op.Record(gtx.Ops)
					d := rounded(bgtx, colorMenu, 12, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx,
							oneLine(label(th, 13, colorText, bubble)).Layout)
					})
					call := m.Stop()
					by := bannerH + gtx.Dp(6)
					bt := op.Offset(image.Pt(bx, by)).Push(gtx.Ops)
					call.Add(gtx.Ops)
					bt.Pop()
					paint.FillShape(gtx.Ops, colorMenu, clip.Ellipse(image.Rect(bx-gtx.Dp(6), by+d.Size.Y-gtx.Dp(4), bx+gtx.Dp(2), by+d.Size.Y+gtx.Dp(4))).Op(gtx.Ops))
				}
				return layout.Dimensions{Size: size}
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(head),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(10), Bottom: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						rows := []layout.FlexChild{
							layout.Rigid(bold(label(th, 20, colorHeader, name)).Layout),
							layout.Rigid(label(th, 14, colorText, f.username).Layout),
						}
						if f.pronouns != "" {
							rows = append(rows, layout.Rigid(label(th, 13, colorMuted, f.pronouns).Layout))
						}
						if f.bio != "" {
							rows = append(rows,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 10) }),
								layout.Rigid(bold(label(th, 12, colorMuted, "ABOUT ME")).Layout),
								layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
								layout.Rigid(label(th, 14, colorText, f.bio).Layout),
							)
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					})
				}),
			)
		})
	})
}

// socialRows are the Content & Social page.
func (c *chatView) socialRows() []layout.Widget {
	th := c.u.th
	a := &c.settings.acct
	if !a.loaded {
		return c.accountNote()
	}
	st := a.settings
	fs := st.FriendSourceFlags
	filters := []struct{ name, about string }{
		{"Keep me safe", "Scan direct messages from everyone for explicit content."},
		{"My friends are nice", "Scan direct messages from everyone unless they are a friend."},
		{"Do not scan", "Direct messages will not be scanned for explicit content."},
	}
	rows := append(c.accountNote(),
		settingsHeading(th, "CONTENT FILTER"),
		settingsText(th, "Choose whether Discord scans the images you are sent in direct messages for explicit content."),
	)
	for i, fl := range filters {
		v := 2 - i
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &a.filter[i], fl.name+" — "+fl.about, st.ExplicitContentFilter == v)
		})
	}
	return append(rows,
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsHeading(th, "DIRECT MESSAGES"),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &a.dms, "Allow DMs from server members", "Lets members of the servers you join send you direct messages and friend requests. It applies to servers you join from now on.", !st.DefaultGuildsRestricted)
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsHeading(th, "WHO CAN SEND YOU A FRIEND REQUEST"),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &a.frAll, "Everyone", "", fs.All)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &a.frFriends, "Friends of Friends", "", fs.All || fs.MutualFriends)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &a.frGuilds, "Server Members", "Members of the servers you are in.", fs.All || fs.MutualGuilds)
		},
	)
}

// privacyRows are the Data & Privacy page.
func (c *chatView) privacyRows() []layout.Widget {
	th := c.u.th
	a := &c.settings.acct
	if !a.loaded {
		return c.accountNote()
	}
	return append(c.accountNote(),
		settingsHeading(th, "ACTIVITY"),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &a.game, "Share my activity", "Show what you are playing or doing, such as games shown through Rich Presence, on your profile.", a.settings.ShowCurrentGame)
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsText(th, "Discord keeps its newer privacy settings, such as who sees your full profile, in a format Linkcord cannot change yet; change those in Discord's own app or website."),
	)
}

// layoutSettingsUser is the user's card at the top of the settings'
// sidebar, which opens their profile to edit.
func (c *chatView) layoutSettingsUser(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	a := &c.settings.acct
	me := c.me
	name := me.DisplayName
	if name == "" {
		name = me.Username
	}
	return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return rowItem(gtx, &a.editProfile, func(gtx layout.Context, hovered bool) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.avatarStatus(gtx, me, discord.NullGuildID, 40, colorSidebar)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, stacked(
					oneLine(bold(label(th, 15, colorHeader, name))).Layout,
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(label(th, 13, colorMuted, "Edit Profiles").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return icon(gtx, iconEdit, 14, colorMuted)
								})
							}),
						)
					},
				)),
			)
		}, c.settings.page == pageProfiles)
	})
}
