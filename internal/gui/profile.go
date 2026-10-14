package gui

import (
	"cmp"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
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
		// ThemeColors are the profile's two colors, top and bottom, which
		// its card fades between.
		ThemeColors []int `json:"theme_colors"`
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
		Bio         string `json:"bio"`
		Pronouns    string `json:"pronouns"`
		Banner      string `json:"banner"`
		ThemeColors []int  `json:"theme_colors"`
	} `json:"guild_member_profile"`
	// The servers and friends the user and the one shown have in common.
	MutualGuilds []struct {
		ID discord.GuildID `json:"id"`
	} `json:"mutual_guilds"`
	MutualFriends      []discord.User `json:"mutual_friends"`
	MutualFriendsCount int            `json:"mutual_friends_count"`
}

// themeColors are the two colors of the profile, top and bottom, darkened as
// Discord shows them behind the text, or false if it has none.
func (d *profileData) themeColors(guildID discord.GuildID) (top, bottom colorT, ok bool) {
	if d == nil {
		return
	}
	colors := d.UserProfile.ThemeColors
	if gm := d.GuildMemberProfile; gm != nil && len(gm.ThemeColors) == 2 && guildID.IsValid() {
		colors = gm.ThemeColors
	}
	if len(colors) != 2 {
		return
	}
	dark := func(v int) colorT {
		c := rgb(uint32(v))
		mix := func(x uint8) uint8 { return uint8(float32(x) * 0.45) }
		return colorT{R: mix(c.R), G: mix(c.G), B: mix(c.B), A: 0xff}
	}
	return dark(colors[0]), dark(colors[1]), true
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

	backdrop widget.Clickable
	// avatarBtn opens the avatar big in the viewer.
	avatarBtn widget.Clickable
	// opened is when the card opened.
	opened time.Time
	// bannerBtn shows the banner, bannerURL, big, as the avatar.
	bannerBtn        widget.Clickable
	bannerURL        string
	bannerW          int
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
	// joinVoice joins the voice channel they are in, and watchStream
	// watches their stream.
	joinVoice, watchStream widget.Clickable
	// saveBanner saves the banner to the downloads.
	saveBanner widget.Clickable
	// friend adds, accepts or removes them as a friend, unblock unblocks
	// them, and more opens the menu of the user.
	friend, unblock, more widget.Clickable
	// The lists of servers and friends in common, shown when clicked.
	mutualGuildsBtn, mutualFriendsBtn widget.Clickable
	showGuilds, showFriends           bool
	mutualClicks                      map[discord.Snowflake]*widget.Clickable
	scroll                            widget.List
	bio                               widget.Selectable
}

// openProfile shows the card of user, as a member of guildID if valid.
func (c *chatView) openProfile(user discord.User, guildID discord.GuildID) {
	p := &profileView{user: user, guildID: guildID, self: user.ID == c.me.ID, loading: true, opened: time.Now()}
	p.scroll.Axis = layout.Vertical
	c.profile = p

	key := profileKey{user.ID, guildID}
	if c.demo {
		p.loading = false
		if !p.self {
			p.data = c.demoProfile(user)
		}
		return
	}
	if data, ok := c.profiles[key]; ok {
		p.data, p.loading = data, false
		return
	}
	go func() {
		mutual := strconv.FormatBool(user.ID != c.me.ID)
		q := url.Values{
			"with_mutual_guilds":        {mutual},
			"with_mutual_friends":       {mutual},
			"with_mutual_friends_count": {mutual},
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

// demoProfile is a profile for the demo, with theme colors and what is in
// common, to show them.
func (c *chatView) demoProfile(user discord.User) *profileData {
	d := &profileData{}
	d.User.User = user
	d.UserProfile.Bio = "Just a demo profile."
	d.UserProfile.ThemeColors = []int{0xc4163d, 0x3b3b3b}
	if guilds, _ := c.st.Cabinet.Guilds(); len(guilds) > 0 {
		for _, g := range guilds[:min(len(guilds), 3)] {
			d.MutualGuilds = append(d.MutualGuilds, struct {
				ID discord.GuildID `json:"id"`
			}{g.ID})
		}
	}
	for _, r := range c.relationships() {
		if r.Type == discord.FriendRelationship && r.UserID != user.ID && len(d.MutualFriends) < 4 {
			d.MutualFriends = append(d.MutualFriends, r.User)
		}
	}
	d.MutualFriendsCount = len(d.MutualFriends)
	return d
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
	if p.avatarBtn.Clicked(gtx) {
		c.openAvatar(p.user, p.guildID)
	}
	if p.bannerBtn.Clicked(gtx) && p.bannerURL != "" {
		// Discord's banners are 680×240 at their smallest.
		big := bannerBig(p.bannerURL)
		c.openViewer("", big, 2048, 723, big, p.bannerURL, p.bannerW)
	}
	if p.saveBanner.Clicked(gtx) && p.bannerURL != "" {
		c.saveFile(bannerBig(p.bannerURL), "")
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
	if p.friend.Clicked(gtx) {
		user := p.user
		switch c.relationship(user.ID) {
		case discord.FriendRelationship:
			c.prompt("Remove '"+user.DisplayOrUsername()+"'", "Remove Friend", true,
				[]string{"Are you sure you want to remove " + user.DisplayOrUsername() + " from your friends?"}, nil,
				func([]string) { c.setFriend(user, false) })
		case discord.SentFriendRequest:
			c.setFriend(user, false)
		default:
			c.setFriend(user, true)
		}
	}
	if p.unblock.Clicked(gtx) {
		c.setBlocked(p.user, false)
	}
	if p.more.Clicked(gtx) {
		c.openUserMenu(p.user.ID, p.guildID, 0)
	}
	if p.watchStream.Clicked(gtx) {
		if ch, ok := c.userVoiceChannel(p.guildID, p.user.ID); ok {
			if vs, err := c.st.Cabinet.VoiceState(ch.GuildID, p.user.ID); err == nil {
				c.profile = nil
				c.startWatching(*vs)
				return
			}
		}
	}
	if p.joinVoice.Clicked(gtx) {
		if ch, ok := c.userVoiceChannel(p.guildID, p.user.ID); ok {
			c.profile = nil
			c.joinVoice(ch)
			c.openCall(ch)
			return
		}
	}
	if p.mutualGuildsBtn.Clicked(gtx) {
		p.showGuilds, p.showFriends = !p.showGuilds, false
	}
	if p.mutualFriendsBtn.Clicked(gtx) {
		p.showFriends, p.showGuilds = !p.showFriends, false
	}
	if d := p.data; d != nil {
		for _, g := range d.MutualGuilds {
			if click := p.mutualClicks[discord.Snowflake(g.ID)]; click != nil && click.Clicked(gtx) {
				c.profile = nil
				c.selectGuild(g.ID)
				return
			}
		}
		for _, f := range d.MutualFriends {
			if click := p.mutualClicks[discord.Snowflake(f.ID)]; click != nil && click.Clicked(gtx) {
				c.openProfile(f, discord.NullGuildID)
				return
			}
		}
	}
}

// setStatus changes the status of the user, keeping their custom status.
func (c *chatView) setStatus(s discord.Status) {
	// Shown at once: Discord does not tell the user their own presence.
	c.myStatus = s
	c.setMyPresence(s, nil)
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
	if top, bottom, ok := p.data.themeColors(p.guildID); ok {
		st := clip.UniformRRect(r, gtx.Dp(8)).Push(gtx.Ops)
		paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: top, Stop2: f32.Pt(0, float32(r.Max.Y)), Color2: bottom}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		st.Pop()
	} else {
		paint.FillShape(gtx.Ops, colorPopup, clip.UniformRRect(r, gtx.Dp(8)).Op(gtx.Ops))
	}
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
		} else if len(d.UserProfile.ThemeColors) == 2 {
			// A themed profile's banner is its first color.
			bannerColor = rgb(uint32(d.UserProfile.ThemeColors[0]))
		}
		hash := d.UserProfile.Banner
		if hash == "" {
			hash = string(d.User.Banner)
		}
		// An animated banner, whose hash starts with a_, is a GIF, which moves.
		ext := func(hash string) string {
			if strings.HasPrefix(hash, "a_") {
				return ".gif?size=600"
			}
			return ".png?size=600"
		}
		if gm := d.GuildMemberProfile; gm != nil && gm.Banner != "" && p.guildID.IsValid() {
			bannerURL = "https://cdn.discordapp.com/guilds/" + p.guildID.String() + "/users/" + user.ID.String() + "/banners/" + gm.Banner + ext(gm.Banner)
		} else if hash != "" {
			bannerURL = "https://cdn.discordapp.com/banners/" + user.ID.String() + "/" + hash + ext(hash)
		}
	}
	bannerH := gtx.Dp(64)
	if bannerURL != "" {
		bannerH = width * 10 / 28
	}

	ringColor := colorPopup
	if top, _, ok := p.data.themeColors(p.guildID); ok {
		ringColor = top
	}
	avatarSize := unit.Dp(80)
	avatarPx := gtx.Dp(avatarSize)
	ringPx := gtx.Dp(6)
	headerH := bannerH + avatarPx/2 + ringPx

	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(width, headerH)
			paint.FillShape(gtx.Ops, bannerColor, clip.Rect{Max: image.Pt(width, bannerH)}.Op())
			if img := c.u.images.frame(gtx, bannerURL, width); img != nil {
				bgtx := gtx
				bgtx.Constraints = layout.Exact(image.Pt(width, bannerH))
				st := clip.Rect{Max: image.Pt(width, bannerH)}.Push(gtx.Ops)
				widget.Image{Src: *img, Fit: widget.Cover}.Layout(bgtx)
				st.Pop()
			}
			p.bannerURL, p.bannerW = bannerURL, width
			if bannerURL != "" {
				// A click on the banner shows it big, to look at or save.
				p.bannerBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return layout.Dimensions{Size: image.Pt(width, bannerH)}
				})
				// And a button saves it, a Linkcord feature.
				if !c.u.app.NoBannerDownload {
					s := gtx.Dp(28)
					left := gtx.Dp(8)
					if !p.self {
						left = gtx.Dp(44)
					}
					t := op.Offset(image.Pt(width-s-left, gtx.Dp(8))).Push(gtx.Ops)
					bgtx := gtx
					bgtx.Constraints = layout.Exact(image.Pt(s, s))
					p.saveBanner.Layout(bgtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						a := uint8(0x80)
						if p.saveBanner.Hovered() {
							a = 0xc0
						}
						paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), a), clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconDownload, 18, colorHeader) })
					})
					t.Pop()
				}
			}
			// The avatar sits on the edge of the banner, in a ring of the
			// card's color.
			x, y := gtx.Dp(16), bannerH-avatarPx/2-ringPx
			ring := image.Rect(x, y, x+avatarPx+2*ringPx, y+avatarPx+2*ringPx)
			paint.FillShape(gtx.Ops, ringColor, clip.Ellipse(ring).Op(gtx.Ops))
			t := op.Offset(image.Pt(x+ringPx, y+ringPx)).Push(gtx.Ops)
			c.avatarStatus(gtx, user, p.guildID, avatarSize, ringColor)
			// A click on the avatar shows it big, to look at or save.
			p.avatarBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				defer clip.Ellipse(image.Rect(0, 0, avatarPx, avatarPx)).Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return layout.Dimensions{Size: image.Pt(avatarPx, avatarPx)}
			})
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
		section(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(bold(label(th, 20, colorHeader, name)).Layout),
				layout.Rigid(c.linkcordBadge(user.ID, 20)),
			)
		}),
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

	// Friends and servers in common, which open their lists.
	if d := p.data; d != nil && !p.self {
		if mutual := c.layoutMutuals(p, d); mutual != nil {
			children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout), section(mutual))
		}
	}

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
		for _, a := range latestActivities(presence.Activities) {
			if a.Type == discord.CustomActivity {
				continue
			}
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				section(func(gtx layout.Context) layout.Dimensions { return c.layoutActivity(gtx, a) }),
			)
		}
	}

	// The voice channel they are in, as Discord shows it.
	if card := c.layoutVoiceCard(p, user.ID); card != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			section(card),
		)
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
		rel := c.relationship(user.ID)
		children = append(children, section(func(gtx layout.Context) layout.Dimensions {
			main := func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				if rel == discord.BlockedRelationship {
					return button(gtx, th, &p.unblock, colorRed, "Unblock")
				}
				return button(gtx, th, &p.message, colorAccent, "Send message")
			}
			children := []layout.FlexChild{layout.Flexed(1, main)}
			if rel != discord.BlockedRelationship && !user.Bot {
				// Friends, as Discord's button: add, accept, pending or
				// friends.
				ic, col := iconMenuInvite, colorHeader
				switch rel {
				case discord.FriendRelationship:
					ic, col = iconFriends, colorGreen
				case discord.SentFriendRequest:
					ic, col = iconPending, colorMuted
				case discord.IncomingFriendRequest:
					ic, col = iconCheck, colorGreen
				}
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconBox(gtx, &p.friend, ic, col)
				}))
			}
			children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconBox(gtx, &p.more, iconMore, colorHeader)
			}))
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		}))
	}
	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout))

	gtx.Constraints.Min.X = width
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// userVoiceChannel is the voice channel the user is in, in the server
// given, or else in any server known.
func (c *chatView) userVoiceChannel(guildID discord.GuildID, id discord.UserID) (discord.Channel, bool) {
	guilds := append([]discord.GuildID{guildID}, c.guildOrder...)
	for _, g := range guilds {
		if !g.IsValid() {
			continue
		}
		if vs, err := c.st.Cabinet.VoiceState(g, id); err == nil && vs.ChannelID.IsValid() {
			if ch, err := c.st.Cabinet.Channel(vs.ChannelID); err == nil {
				return *ch, true
			}
		}
	}
	return discord.Channel{}, false
}

// layoutVoiceCard is the card of the voice channel someone is in, with who
// is there and a button to join, or nil when they are in none known.
func (c *chatView) layoutVoiceCard(p *profileView, id discord.UserID) layout.Widget {
	ch, ok := c.userVoiceChannel(p.guildID, id)
	if !ok {
		return nil
	}
	th := c.u.th
	gname := ""
	if g, err := c.st.Cabinet.Guild(ch.GuildID); err == nil {
		gname = g.Name
	}
	states, _ := c.st.Cabinet.VoiceStates(ch.GuildID)
	var there []discord.VoiceState
	for _, vs := range states {
		if vs.ChannelID == ch.ID {
			there = append(there, vs)
		}
	}
	// The same ones every frame, whoever the profile is of first: the voice
	// states come in no order, and the pictures changed each frame.
	slices.SortFunc(there, func(a, b discord.VoiceState) int {
		if (a.UserID == id) != (b.UserID == id) {
			if a.UserID == id {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.UserID, b.UserID)
	})
	status := c.voiceStatuses[ch.ID]
	joined := c.u.voiceChannel() == ch.ID
	// Their stream, if they share their screen, as Discord shows it.
	var stream *discord.VoiceState
	game := ""
	if vs, err := c.st.Cabinet.VoiceState(ch.GuildID, id); err == nil && vs.SelfStream {
		stream = vs
		if pr := c.presence(ch.GuildID, id); pr != nil {
			for _, a := range pr.Activities {
				if a.Type == discord.GameActivity {
					game = a.Name
				}
			}
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		bg := colorMenu
		if _, _, themed := p.data.themeColors(p.guildID); themed {
			bg = withAlpha(rgb(0x000000), 0x66)
		}
		return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				title := "In voice channel"
				if stream != nil {
					title = "Streaming"
					if game != "" {
						title += " " + game
					}
				}
				rows := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(bold(label(th, 12, colorText, title)).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if stream == nil {
									return layout.Dimensions{}
								}
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return liveBadge(gtx, th) })
							}),
						)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				}
				if stream != nil {
					key := c.streamKeyOf(*stream)
					rows = append(rows,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.layoutPreviewPicture(gtx, key, gtx.Constraints.Max.X)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					)
				}
				rows = append(rows,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							// Who is there, overlapping, in a box as Discord's.
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								size := gtx.Dp(60)
								paint.FillShape(gtx.Ops, colorServers, clip.UniformRRect(image.Rectangle{Max: image.Pt(size, size)}, gtx.Dp(8)).Op(gtx.Ops))
								n := min(len(there), 2)
								for i := 0; i < n; i++ {
									vs := there[i]
									s := 26
									t := offset(gtx, gtx.Dp(6)+i*gtx.Dp(22), gtx.Dp(6)+i*gtx.Dp(22))
									c.u.picture(gtx, c.voiceAvatarURL(ch.GuildID, vs), unit.Dp(s), colorFor(uint64(vs.UserID)), c.memberName(ch.GuildID, vs.UserID, vs.Member), -1)
									t.Pop()
								}
								return layout.Dimensions{Size: image.Pt(size, size)}
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								lines := []layout.FlexChild{
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
											layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconVoice, 14, colorHeader) }),
											layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
											layout.Flexed(1, oneLine(bold(label(th, 14, colorHeader, ch.Name))).Layout),
										)
									}),
								}
								if gname != "" {
									lines = append(lines, layout.Rigid(oneLine(label(th, 12, colorText, "in "+gname)).Layout))
								}
								if status != "" {
									lines = append(lines, layout.Rigid(oneLine(label(th, 12, colorMuted, status)).Layout))
								}
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
							}),
						)
					}),
				)
				switch {
				case joined && stream != nil && !c.watching(id):
					rows = append(rows,
						layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return button(gtx, th, &p.watchStream, colorGreenButton, "Watch Stream")
						}),
					)
				case !joined:
					rows = append(rows,
						layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return button(gtx, th, &p.joinVoice, colorSecondaryButton, "Join Voice Chat")
						}),
					)
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			})
		})
	}
}

var (
	iconFriends = mustIcon(icons.SocialPeople)
	iconPending = mustIcon(icons.ActionSchedule)
)

// bannerBig is a banner's URL at its biggest: its URL already says a size,
// which a second would not replace.
func bannerBig(url string) string {
	base, _, _ := strings.Cut(url, "?")
	return base + "?size=2048"
}

// iconBox is a square button with an icon, as the buttons beside Send
// message.
func iconBox(gtx layout.Context, click *widget.Clickable, ic *widget.Icon, col colorT) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		bg := colorSelected
		if click.Hovered() {
			bg = colorHover
		}
		size := image.Pt(gtx.Dp(38), gtx.Dp(36))
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, ic, 20, col) })
	})
}

// layoutMutuals is the line of friends and servers in common, and the list
// of either when clicked, or nil when there are none.
func (c *chatView) layoutMutuals(p *profileView, d *profileData) layout.Widget {
	th := c.u.th
	friends := max(d.MutualFriendsCount, len(d.MutualFriends))
	guilds := len(d.MutualGuilds)
	if friends == 0 && guilds == 0 {
		return nil
	}
	if p.mutualClicks == nil {
		p.mutualClicks = map[discord.Snowflake]*widget.Clickable{}
	}
	link := func(click *widget.Clickable, txt string, lead layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				col := colorMuted
				if click.Hovered() {
					col = colorHeader
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(lead), layout.Rigid(label(th, 13, col, txt).Layout))
			})
		})
	}
	row := func(key discord.Snowflake, pic layout.Widget, name string) layout.FlexChild {
		click := p.mutualClicks[key]
		if click == nil {
			click = new(widget.Clickable)
			p.mutualClicks[key] = click
		}
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
				return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(pic),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, oneLine(label(th, 14, colorText, name)).Layout),
					)
				})
			}, false)
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		var line []layout.FlexChild
		if friends > 0 {
			// The first friends' avatars, overlapping, as in Discord.
			faces := func(gtx layout.Context) layout.Dimensions {
				n := min(len(d.MutualFriends), 3)
				if n == 0 {
					return layout.Dimensions{}
				}
				s, step := gtx.Dp(18), gtx.Dp(12)
				for i := n - 1; i >= 0; i-- {
					u := d.MutualFriends[i]
					t := op.Offset(image.Pt(i*step, 0)).Push(gtx.Ops)
					c.u.picture(gtx, cdnSize(u.AvatarURLWithType(discord.PNGImage)), 18, colorFor(uint64(u.ID)), u.DisplayOrUsername(), -1)
					t.Pop()
				}
				return layout.Dimensions{Size: image.Pt((n-1)*step+s+gtx.Dp(6), s)}
			}
			line = append(line, link(&p.mutualFriendsBtn, fmt.Sprintf("%d Mutual Friend%s", friends, plural(friends)), faces))
		}
		if friends > 0 && guilds > 0 {
			line = append(line, layout.Rigid(label(th, 13, colorMuted, " • ").Layout))
		}
		if guilds > 0 {
			line = append(line, link(&p.mutualGuildsBtn, fmt.Sprintf("%d Mutual Server%s", guilds, plural(guilds)), func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{}
			}))
		}
		rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, line...)
		})}
		if p.showFriends {
			rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout))
			for _, u := range d.MutualFriends {
				u := u
				rows = append(rows, row(discord.Snowflake(u.ID), func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, cdnSize(u.AvatarURLWithType(discord.PNGImage)), 28, colorFor(uint64(u.ID)), u.DisplayOrUsername(), -1)
				}, u.DisplayOrUsername()))
			}
		}
		if p.showGuilds {
			rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout))
			for _, mg := range d.MutualGuilds {
				g, err := c.st.Cabinet.Guild(mg.ID)
				if err != nil {
					continue
				}
				name, icon := g.Name, cdnSize(g.IconURLWithType(discord.PNGImage))
				rows = append(rows, row(discord.Snowflake(mg.ID), func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, icon, 28, colorSidebar, name, gtx.Dp(8))
				}, name))
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	}
}

// guildTag is the tag of a server that the user wears next to their name,
// which opens the server's card when clicked, as on Discord.
func (c *chatView) guildTag(gtx layout.Context, guildID discord.GuildID, badge, tag string) layout.Dimensions {
	if c.tagClicks == nil {
		c.tagClicks = make(map[discord.GuildID]*widget.Clickable)
	}
	cl := clickOf(c.tagClicks, guildID)
	if cl.Clicked(gtx) {
		c.u.post(func() { c.openTagCard(guildID, badge, tag) })
	}
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return c.guildTagChip(gtx, guildID, badge, tag)
	})
}

// guildTagChip is the look of a server tag.
func (c *chatView) guildTagChip(gtx layout.Context, guildID discord.GuildID, badge, tag string) layout.Dimensions {
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
	var start, end time.Time
	if a.Timestamps != nil && a.Timestamps.Start > 0 {
		start = a.Timestamps.Start.Time()
		if a.Timestamps.End > a.Timestamps.Start {
			end = a.Timestamps.End.Time()
		}
		// Redraw every second for the time counter.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
	}
	var img string
	if a.Assets != nil {
		img = activityImageURL(a, a.Assets.LargeImage)
	}
	// On a profile's colors the box lets them through.
	bg := colorMenu
	if p := c.profile; p != nil {
		if _, _, ok := p.data.themeColors(p.guildID); ok {
			bg = withAlpha(rgb(0x000000), 0x66)
		}
	}
	return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
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
							switch {
							case !end.IsZero():
								lines = append(lines, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return trackProgress(gtx, th, start, end, gtx.Now)
									})
								}))
							case !start.IsZero():
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

// trackProgress is how far a song with an end is, as Discord shows it: the
// time played, a bar, and how long it is.
func trackProgress(gtx layout.Context, th *material.Theme, start, end, now time.Time) layout.Dimensions {
	total := end.Sub(start)
	played := min(max(now.Sub(start), 0), total)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(label(th, 12, colorText, trackTime(played)).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				w, h := gtx.Constraints.Max.X, max(gtx.Dp(4), 2)
				bar := image.Rect(0, 0, w, h)
				paint.FillShape(gtx.Ops, withAlpha(colorHeader, 0x40), clip.UniformRRect(bar, h/2).Op(gtx.Ops))
				done := image.Rect(0, 0, int(float64(w)*float64(played)/float64(total)), h)
				paint.FillShape(gtx.Ops, colorHeader, clip.UniformRRect(done, h/2).Op(gtx.Ops))
				return layout.Dimensions{Size: image.Pt(w, h)}
			})
		}),
		layout.Rigid(label(th, 12, colorText, trackTime(total)).Layout),
	)
}

// trackTime is a length of a song as 03:41, or 1:03:41 past an hour.
func trackTime(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// layoutRoles shows the roles of a member as chips with their color.
func (c *chatView) layoutRoles(gtx layout.Context, guildID discord.GuildID, ids []discord.RoleID) layout.Dimensions {
	th := c.u.th
	var children []layout.FlexChild
	// The highest role first, as on Discord: by position, then the older
	// role first among equals.
	var roles []discord.Role
	for _, id := range ids {
		if role, err := c.st.Cabinet.Role(guildID, id); err == nil {
			roles = append(roles, *role)
		}
	}
	slices.SortStableFunc(roles, func(a, b discord.Role) int {
		if a.Position != b.Position {
			return b.Position - a.Position
		}
		return cmp.Compare(a.ID, b.ID)
	})
	for _, role := range roles {
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

// openAvatar shows the avatar of user, as it shows in guildID, big in the
// viewer, from which it can be saved; an animated one moves.
func (c *chatView) openAvatar(user discord.User, guildID discord.GuildID) {
	hash, url := user.Avatar, user.AvatarURLWithType(discord.PNGImage)
	if guildID.IsValid() {
		if m, err := c.st.Cabinet.Member(guildID, user.ID); err == nil && m.Avatar != "" {
			hash, url = m.Avatar, m.AvatarURLWithType(discord.PNGImage, guildID)
		}
	}
	if hash == "" {
		// The default avatar is small, and no bigger exists.
		c.openViewer("", url, 256, 256, url, "", 0)
		return
	}
	if strings.HasPrefix(hash, "a_") {
		url = strings.TrimSuffix(url, ".png") + ".gif"
	}
	thumb := c.authorAvatarURL(guildID, user)
	c.openViewer("", url+"?size=1024", 1024, 1024, url+"?size=1024", thumb, 128)
}
