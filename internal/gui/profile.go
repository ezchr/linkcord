package gui

import (
	"image"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/clipboard"
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
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconChevron = mustIcon(icons.NavigationChevronRight)
	iconCopy    = mustIcon(icons.ContentContentCopy)
	iconQuit    = mustIcon(icons.ActionPowerSettingsNew)
	iconClose   = mustIcon(icons.NavigationClose)
	iconMembers = mustIcon(icons.SocialPeople)
)

// profileData is what the profile endpoint returns that the card shows.
type profileData struct {
	User struct {
		discord.User
		PrimaryGuild *struct {
			GuildID discord.GuildID `json:"identity_guild_id"`
			Enabled bool            `json:"identity_enabled"`
			Tag     string          `json:"tag"`
			Badge   string          `json:"badge"`
		} `json:"primary_guild"`
	} `json:"user"`
	UserProfile struct {
		Bio         string `json:"bio"`
		Pronouns    string `json:"pronouns"`
		AccentColor *int   `json:"accent_color"`
		Banner      string `json:"banner"`
	} `json:"user_profile"`
	Badges []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	} `json:"badges"`
	GuildMember *struct {
		JoinedAt discord.Timestamp `json:"joined_at"`
	} `json:"guild_member"`
	GuildMemberProfile *struct {
		Bio      string `json:"bio"`
		Pronouns string `json:"pronouns"`
		Banner   string `json:"banner"`
	} `json:"guild_member_profile"`
}

// statusChoices are the statuses the user can pick, as Discord offers them.
var statusChoices = []struct {
	status discord.Status
	desc   string
}{
	{discord.OnlineStatus, ""},
	{discord.IdleStatus, ""},
	{discord.DoNotDisturbStatus, "You will not receive desktop notifications."},
	{discord.InvisibleStatus, "You will appear offline."},
}

type profileView struct {
	user    discord.User
	guildID discord.GuildID
	self    bool

	data    *profileData
	loading bool

	backdrop         widget.Clickable
	closeBtn         widget.Clickable
	message          widget.Clickable
	copyID           widget.Clickable
	logout           widget.Clickable
	quit             widget.Clickable
	statusBtn        widget.Clickable
	performanceBtn   widget.Clickable
	performanceError string
	statusMenu       bool
	statusOpts       [4]widget.Clickable
	copied           bool
	scroll           widget.List
	bio              widget.Selectable
}

// openProfile shows the card of user, as a member of guildID if valid.
func (c *chatView) openProfile(user discord.User, guildID discord.GuildID) {
	p := &profileView{user: user, guildID: guildID, self: user.ID == c.me.ID, loading: true}
	p.scroll.Axis = layout.Vertical
	c.profile = p

	key := profileKey{user.ID, guildID}
	if c.demo {
		p.loading = false
		return
	}
	if data, ok := c.profiles[key]; ok {
		p.data, p.loading = data, false
		return
	}
	go func() {
		q := url.Values{
			"with_mutual_guilds":        {"false"},
			"with_mutual_friends":       {"false"},
			"with_mutual_friends_count": {"false"},
		}
		if guildID.IsValid() {
			q.Set("guild_id", guildID.String())
		}
		data, err := c.st.Client.RequestJSON[*profileData]("GET", api.EndpointUsers+user.ID.String()+"/profile?"+q.Encode())
		c.u.post(func() {
			p.loading = false
			if err != nil {
				slog.Warn("failed to load profile", "err", err, "user_id", user.ID)
				return
			}
			c.profiles[key] = data
			p.data = data
		})
	}()
}

type profileKey struct {
	user  discord.UserID
	guild discord.GuildID
}

func (c *chatView) updateProfile(gtx layout.Context) {
	p := c.profile
	if p.backdrop.Clicked(gtx) || p.closeBtn.Clicked(gtx) {
		c.profile = nil
		return
	}
	if p.copyID.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(p.user.ID.String()))})
		p.copied = true
	}
	if p.statusBtn.Clicked(gtx) {
		p.statusMenu = !p.statusMenu
	}
	if p.self && p.performanceBtn.Clicked(gtx) {
		p.performanceError = ""
		if err := c.u.setPerformanceMode(!c.u.images.lowPower); err != nil {
			p.performanceError = "Could not save Performance Mode"
			slog.Warn("could not save performance mode", "err", err)
		}
	}
	for i := range p.statusOpts {
		if p.statusOpts[i].Clicked(gtx) {
			c.setStatus(statusChoices[i].status)
			p.statusMenu = false
		}
	}
	if p.quit.Clicked(gtx) {
		c.profile = nil
		c.u.quit()
		return
	}
	if p.logout.Clicked(gtx) {
		c.profile = nil
		c.u.logout("")
		return
	}
	if p.message.Clicked(gtx) {
		c.profile = nil
		c.openDM(p.user.ID)
	}
}

// setStatus changes the status of the user, keeping their custom status.
func (c *chatView) setStatus(s discord.Status) {
	go func() {
		if err := c.st.SetStatus(s, nil); err != nil {
			slog.Error("failed to set status", "err", err)
		}
		c.u.win.Invalidate()
	}()
}

// openDM opens the direct messages with a user, creating them if needed.
func (c *chatView) openDM(userID discord.UserID) {
	go func() {
		ch, err := c.st.CreatePrivateChannel(userID)
		if err != nil {
			slog.Error("failed to open direct message", "err", err)
			return
		}
		c.u.post(func() {
			if c.closed {
				return
			}
			if c.selGuild.IsValid() {
				c.selectGuild(discord.NullGuildID)
			}
			c.rebuildRows()
			c.selectChannel(*ch)
		})
	}()
}

// layoutProfile lays out the open profile card over the chat: above the user
// panel for the user's own, in the middle for others.
func (c *chatView) layoutProfile(gtx layout.Context) layout.Dimensions {
	c.updateProfile(gtx)
	p := c.profile
	if p == nil {
		return layout.Dimensions{}
	}
	size := gtx.Constraints.Max
	// Clicking outside the card closes it.
	p.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !p.self {
			paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x99), clip.Rect{Max: size}.Op())
		}
		return layout.Dimensions{Size: size}
	})

	margin := gtx.Dp(12)
	cardW := min(gtx.Dp(340), size.X-2*margin)
	bottom := size.Y - margin
	if p.self {
		bottom = size.Y - gtx.Dp(60)
	}
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(cardW, 0), Max: image.Pt(cardW, bottom-margin)}
	macro := op.Record(gtx.Ops)
	dims := c.layoutCard(cgtx, p)
	call := macro.Stop()

	pos := image.Pt((size.X-cardW)/2, (size.Y-dims.Size.Y)/2)
	if p.self {
		pos = image.Pt(gtx.Dp(8), bottom-dims.Size.Y)
	}
	t := op.Offset(pos).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()

	if p.self && p.statusMenu {
		mgtx := gtx
		mgtx.Constraints = layout.Constraints{Min: image.Pt(gtx.Dp(300), 0), Max: image.Pt(gtx.Dp(300), size.Y)}
		macro := op.Record(gtx.Ops)
		mdims := c.layoutStatusMenu(mgtx, p)
		call := macro.Stop()
		x := pos.X + cardW + gtx.Dp(8)
		if x+mdims.Size.X > size.X {
			x = size.X - mdims.Size.X - margin
		}
		t := op.Offset(image.Pt(x, max(pos.Y+dims.Size.Y-mdims.Size.Y-gtx.Dp(70), margin))).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
	return layout.Dimensions{Size: size}
}

// solid makes the area of dims take the pointer events, so that clicks on a
// popup do not reach the backdrop behind it.
func solid(gtx layout.Context, tag event.Tag, dims layout.Dimensions) {
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Release | pointer.Scroll}); !ok {
			break
		}
	}
}

func (c *chatView) layoutCard(gtx layout.Context, p *profileView) layout.Dimensions {
	th := c.u.th
	macro := op.Record(gtx.Ops)
	dims := func() layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, gtx.Dp(8)).Push(gtx.Ops).Pop()
		return listLayout(gtx, th, &p.scroll, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return c.layoutCardContent(gtx, p)
		})
	}()
	call := macro.Stop()
	r := image.Rectangle{Max: dims.Size}
	paint.FillShape(gtx.Ops, colorPopup, clip.UniformRRect(r, gtx.Dp(8)).Op(gtx.Ops))
	solid(gtx, p, dims)
	call.Add(gtx.Ops)
	return dims
}

func (c *chatView) layoutCardContent(gtx layout.Context, p *profileView) layout.Dimensions {
	th := c.u.th
	width := gtx.Constraints.Max.X
	user := p.user
	if p.data != nil && p.data.User.ID.IsValid() {
		user = p.data.User.User
	}
	presence := c.presence(p.guildID, user.ID)

	// The banner: the picture of the profile, or its accent color.
	bannerColor := colorFor(uint64(user.ID))
	if user.Accent != 0 {
		bannerColor = rgb(uint32(user.Accent))
	}
	var bannerURL string
	if d := p.data; d != nil {
		if d.UserProfile.AccentColor != nil {
			bannerColor = rgb(uint32(*d.UserProfile.AccentColor))
		}
		hash := d.UserProfile.Banner
		if hash == "" {
			hash = string(d.User.Banner)
		}
		if gm := d.GuildMemberProfile; gm != nil && gm.Banner != "" && p.guildID.IsValid() {
			bannerURL = "https://cdn.discordapp.com/guilds/" + p.guildID.String() + "/users/" + user.ID.String() + "/banners/" + gm.Banner + ".png?size=600"
		} else if hash != "" {
			bannerURL = "https://cdn.discordapp.com/banners/" + user.ID.String() + "/" + hash + ".png?size=600"
		}
	}
	bannerH := gtx.Dp(64)
	if bannerURL != "" {
		bannerH = width * 10 / 28
	}

	avatarSize := unit.Dp(80)
	avatarPx := gtx.Dp(avatarSize)
	ringPx := gtx.Dp(6)
	headerH := bannerH + avatarPx/2 + ringPx

	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(width, headerH)
			paint.FillShape(gtx.Ops, bannerColor, clip.Rect{Max: image.Pt(width, bannerH)}.Op())
			if img := c.u.images.get(bannerURL, width); img != nil {
				bgtx := gtx
				bgtx.Constraints = layout.Exact(image.Pt(width, bannerH))
				st := clip.Rect{Max: image.Pt(width, bannerH)}.Push(gtx.Ops)
				widget.Image{Src: *img, Fit: widget.Cover}.Layout(bgtx)
				st.Pop()
			}
			// The avatar sits on the edge of the banner, in a ring of the
			// card's color.
			x, y := gtx.Dp(16), bannerH-avatarPx/2-ringPx
			ring := image.Rect(x, y, x+avatarPx+2*ringPx, y+avatarPx+2*ringPx)
			paint.FillShape(gtx.Ops, colorPopup, clip.Ellipse(ring).Op(gtx.Ops))
			t := op.Offset(image.Pt(x+ringPx, y+ringPx)).Push(gtx.Ops)
			c.avatarStatus(gtx, user, p.guildID, avatarSize, colorPopup)
			t.Pop()

			if cs := customStatus(presence); cs != "" {
				bx := x + avatarPx + 2*ringPx + gtx.Dp(8)
				bgtx := gtx
				bgtx.Constraints = layout.Constraints{Max: image.Pt(width-bx-gtx.Dp(12), gtx.Dp(60))}
				macro := op.Record(gtx.Ops)
				rounded(bgtx, colorMenu, 10, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := label(th, 13, colorText, cs)
						l.MaxLines = 2
						l.Truncator = "…"
						return l.Layout(gtx)
					})
				})
				call := macro.Stop()
				t := op.Offset(image.Pt(bx, bannerH+gtx.Dp(6))).Push(gtx.Ops)
				call.Add(gtx.Ops)
				t.Pop()
			}

			if !p.self {
				// A round close button over the banner.
				s := gtx.Dp(28)
				t := op.Offset(image.Pt(width-s-gtx.Dp(8), gtx.Dp(8))).Push(gtx.Ops)
				bgtx := gtx
				bgtx.Constraints = layout.Exact(image.Pt(s, s))
				p.closeBtn.Layout(bgtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					a := uint8(0x80)
					if p.closeBtn.Hovered() {
						a = 0xc0
					}
					paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), a), clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, iconClose, 18, colorHeader)
					})
				})
				t.Pop()
			}
			return layout.Dimensions{Size: size}
		}),
	}

	inset := layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}
	section := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return inset.Layout(gtx, w)
		})
	}

	// Name, username, pronouns, tag and badges.
	name := user.DisplayOrUsername()
	if p.guildID.IsValid() {
		name = c.memberName(p.guildID, user.ID, nil)
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		section(bold(label(th, 20, colorHeader, name)).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
		section(func(gtx layout.Context) layout.Dimensions {
			var row []layout.FlexChild
			row = append(row, layout.Rigid(label(th, 14, colorText, user.Username).Layout))
			if d := p.data; d != nil {
				pronouns := d.UserProfile.Pronouns
				if gm := d.GuildMemberProfile; gm != nil && gm.Pronouns != "" {
					pronouns = gm.Pronouns
				}
				if pronouns != "" {
					row = append(row, layout.Rigid(label(th, 14, colorMuted, " · "+pronouns).Layout))
				}
				if pg := d.User.PrimaryGuild; pg != nil && pg.Enabled && pg.Tag != "" {
					row = append(row, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return c.guildTag(gtx, pg.GuildID, pg.Badge, pg.Tag)
						})
					}))
				}
				for _, b := range d.Badges {
					row = append(row, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return c.u.picture(gtx, "https://cdn.discordapp.com/badge-icons/"+b.Icon+".png", 20, colorPopup, "", 0)
						})
					}))
				}
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, row...)
		}),
	)

	// About me.
	if d := p.data; d != nil {
		bio := d.UserProfile.Bio
		if gm := d.GuildMemberProfile; gm != nil && gm.Bio != "" {
			bio = gm.Bio
		}
		if bio != "" {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				section(func(gtx layout.Context) layout.Dimensions {
					l := label(th, 14, colorText, bio)
					l.State = &p.bio
					return l.Layout(gtx)
				}),
			)
		}
	} else if p.loading {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			section(label(th, 13, colorMuted, "Loading profile…").Layout),
		)
	}

	// Activities other than the custom status.
	if presence != nil {
		for _, a := range presence.Activities {
			if a.Type == discord.CustomActivity {
				continue
			}
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				section(func(gtx layout.Context) layout.Dimensions { return c.layoutActivity(gtx, a) }),
			)
		}
	}

	// Since when.
	children = append(children,
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		section(func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{
				layout.Rigid(bold(label(th, 12, colorText, "Member since")).Layout),
				layout.Rigid(label(th, 13, colorText, formatDate(user.CreatedAt())).Layout),
			}
			if d := p.data; d != nil && d.GuildMember != nil && d.GuildMember.JoinedAt.IsValid() {
				gname := "the server"
				if g, err := c.st.Cabinet.Guild(p.guildID); err == nil {
					gname = g.Name
				}
				rows = append(rows,
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(bold(label(th, 12, colorText, "Joined "+gname+" on")).Layout),
					layout.Rigid(label(th, 13, colorText, formatDate(d.GuildMember.JoinedAt.Time())).Layout),
				)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		}),
	)

	// Roles in the server.
	if p.guildID.IsValid() {
		if m, err := c.st.Cabinet.Member(p.guildID, user.ID); err == nil && len(m.RoleIDs) > 0 {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				section(bold(label(th, 12, colorText, "Roles")).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				section(func(gtx layout.Context) layout.Dimensions { return c.layoutRoles(gtx, p.guildID, m.RoleIDs) }),
			)
		}
	}

	// Actions.
	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout))
	if p.self {
		children = append(children, section(func(gtx layout.Context) layout.Dimensions { return c.layoutSelfMenu(gtx, p) }))
	} else {
		children = append(children, section(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return button(gtx, th, &p.message, colorAccent, "Send message")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					txt := "Copy ID"
					if p.copied {
						txt = "Copied!"
					}
					return button(gtx, th, &p.copyID, colorSelected, txt)
				}),
			)
		}))
	}
	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout))

	gtx.Constraints.Min.X = width
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// guildTag is the tag of a server that the user wears next to their name.
func (c *chatView) guildTag(gtx layout.Context, guildID discord.GuildID, badge, tag string) layout.Dimensions {
	th := c.u.th
	return rounded(gtx, colorMenu, 4, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(6), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if badge == "" {
						return layout.Dimensions{}
					}
					url := "https://cdn.discordapp.com/guild-tag-badges/" + guildID.String() + "/" + badge + ".png"
					return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, url, 14, colorMenu, "", 0)
					})
				}),
				layout.Rigid(bold(label(th, 12, colorHeader, tag)).Layout),
			)
		})
	})
}

// layoutActivity is the card of a game, music or stream.
func (c *chatView) layoutActivity(gtx layout.Context, a discord.Activity) layout.Dimensions {
	th := c.u.th
	var start time.Time
	if a.Timestamps != nil && a.Timestamps.Start > 0 {
		start = a.Timestamps.Start.Time()
		// Redraw every second for the time counter.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
	}
	var img string
	if a.Assets != nil {
		img = activityImageURL(a, a.Assets.LargeImage)
	}
	return rounded(gtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(oneLine(bold(label(th, 12, colorText, activityVerb(a)))).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if img == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return c.u.picture(gtx, img, 60, colorSelected, a.Name, gtx.Dp(6))
							})
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							var lines []layout.FlexChild
							title := a.Details
							if title == "" {
								title = a.Name
							}
							lines = append(lines, layout.Rigid(oneLine(bold(label(th, 14, colorHeader, title))).Layout))
							if a.State != "" {
								lines = append(lines, layout.Rigid(oneLine(label(th, 13, colorText, a.State)).Layout))
							}
							if a.Assets != nil && a.Assets.LargeText != "" && a.Assets.LargeText != a.State {
								lines = append(lines, layout.Rigid(oneLine(label(th, 13, colorText, a.Assets.LargeText)).Layout))
							}
							if !start.IsZero() {
								lines = append(lines, layout.Rigid(label(th, 12, colorGreen, "⏱ "+elapsed(start)).Layout))
							}
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
						}),
					)
				}),
			)
		})
	})
}

// layoutRoles shows the roles of a member as chips with their color.
func (c *chatView) layoutRoles(gtx layout.Context, guildID discord.GuildID, ids []discord.RoleID) layout.Dimensions {
	th := c.u.th
	var children []layout.FlexChild
	for _, id := range ids {
		role, err := c.st.Cabinet.Role(guildID, id)
		if err != nil {
			continue
		}
		col := colorOffline
		if role.Color != 0 {
			col = rgb(uint32(role.Color))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorMenu, 4, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(8), Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return dot(gtx, col, 10) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
							layout.Rigid(label(th, 12, colorText, role.Name).Layout),
						)
					})
				})
			})
		}))
	}
	return wrapLayout(gtx, children)
}

// wrapLayout lays out children in rows, wrapping to the next row when one is
// full.
func wrapLayout(gtx layout.Context, children []layout.FlexChild) layout.Dimensions {
	maxW := gtx.Constraints.Max.X
	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	x, y, rowH, width := 0, 0, 0, 0
	for _, child := range children {
		macro := op.Record(gtx.Ops)
		dims := layoutFlexChild(cgtx, child)
		call := macro.Stop()
		if x > 0 && x+dims.Size.X > maxW {
			x, y, rowH = 0, y+rowH, 0
		}
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		x += dims.Size.X
		rowH = max(rowH, dims.Size.Y)
		width = max(width, x)
	}
	return layout.Dimensions{Size: image.Pt(width, y+rowH)}
}

// layoutFlexChild lays out a rigid flex child on its own.
func layoutFlexChild(gtx layout.Context, child layout.FlexChild) layout.Dimensions {
	return layout.Flex{}.Layout(gtx, child)
}

// layoutSelfMenu is the menu of the user's own card: status, ID and logout.
func (c *chatView) layoutSelfMenu(gtx layout.Context, p *profileView) layout.Dimensions {
	th := c.u.th
	status := c.status(discord.NullGuildID, c.me.ID)
	item := func(click *widget.Clickable, lead func(gtx layout.Context) layout.Dimensions, txt string, col colorT, chevron bool) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(lead),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Flexed(1, label(th, 14, col, txt).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !chevron {
							return layout.Dimensions{}
						}
						return icon(gtx, iconChevron, 18, colorMuted)
					}),
				)
			}, click == &p.statusBtn && p.statusMenu)
		})
	}
	performanceText := "Performance Mode: Off"
	if c.u.images.lowPower {
		performanceText = "Performance Mode: On"
	}
	if p.performanceError != "" {
		performanceText = p.performanceError
	}
	copyTxt := "Copy user ID"
	if p.copied {
		copyTxt = "ID copied!"
	}
	return rounded(gtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				item(&p.statusBtn, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return dot(gtx, statusColor(status), 10)
					})
				}, statusName(status), colorText, true),
				item(&p.performanceBtn, func(gtx layout.Context) layout.Dimensions {
					col := colorMuted
					if c.u.images.lowPower {
						col = colorText
					}
					return dot(gtx, col, 10)
				}, performanceText, colorText, false),
				layout.Rigid(label(th, 11, colorMuted, "On uses still GIF/APNG previews.").Layout),
				item(&p.copyID, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconCopy, 18, colorMuted) }, copyTxt, colorText, false),
				item(&p.quit, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconQuit, 18, colorMuted) }, "Quit "+appName, colorText, false),
				item(&p.logout, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconLogout, 18, colorRed) }, "Log out", colorRed, false),
			)
		})
	})
}

// layoutStatusMenu is the menu to pick a status, beside the user's card.
func (c *chatView) layoutStatusMenu(gtx layout.Context, p *profileView) layout.Dimensions {
	th := c.u.th
	macro := op.Record(gtx.Ops)
	dims := rounded(gtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			var items []layout.FlexChild
			for i, choice := range statusChoices {
				items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return rowItem(gtx, &p.statusOpts[i], func(gtx layout.Context, hovered bool) layout.Dimensions {
						return layout.Flex{Alignment: layout.Start}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(2), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									bg := colorMenu
									if p.statusOpts[i].Hovered() {
										bg = colorHover
									}
									return statusIcon(gtx, choice.status, gtx.Dp(12), bg)
								})
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								rows := []layout.FlexChild{layout.Rigid(label(th, 14, colorHeader, statusName(choice.status)).Layout)}
								if choice.desc != "" {
									rows = append(rows, layout.Rigid(label(th, 12, colorMuted, choice.desc).Layout))
								}
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
							}),
						)
					}, false)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
		})
	})
	call := macro.Stop()
	solid(gtx, &p.statusMenu, dims)
	call.Add(gtx.Ops)
	return dims
}

// formatDate is a date as Discord shows it in profiles, such as "12 Oct 2026".
func formatDate(t time.Time) string {
	months := [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	t = t.Local()
	return strconv.Itoa(t.Day()) + " " + months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}
