package gui

import (
	"bytes"
	"embed"
	"encoding/json"
	"image"
	"image/png"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// What a profile says beside the About Me: the accounts linked to it, and
// the games of its widgets (favorite, playing, wanted), as Discord's own
// profile shows them.

// connectedAccount is an account of another service linked to a profile.
type connectedAccount struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
}

// connectionNames are the services as Discord names them.
var connectionNames = map[string]string{
	"battlenet": "Battle.net", "bluesky": "Bluesky", "bungie": "Bungie.net", "crunchyroll": "Crunchyroll",
	"domain": "Domain", "ebay": "eBay", "epicgames": "Epic Games", "facebook": "Facebook", "github": "GitHub",
	"instagram": "Instagram", "leagueoflegends": "League of Legends", "mastodon": "Mastodon", "paypal": "PayPal",
	"playstation": "PlayStation", "reddit": "Reddit", "riotgames": "Riot Games", "roblox": "Roblox",
	"spotify": "Spotify", "steam": "Steam", "tiktok": "TikTok", "twitch": "Twitch", "twitter": "X",
	"xbox": "Xbox", "youtube": "YouTube", "amazon-music": "Amazon Music", "skype": "Skype",
}

func connectionName(kind string) string {
	if n, ok := connectionNames[kind]; ok {
		return n
	}
	if kind == "" {
		return "Account"
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// connectionURL is the page of a linked account, or "" for a service whose
// accounts have none.
func connectionURL(a connectedAccount) string {
	name, id := url.PathEscape(a.Name), url.PathEscape(a.ID)
	switch a.Type {
	case "github":
		return "https://github.com/" + name
	case "twitter":
		return "https://x.com/" + name
	case "twitch":
		return "https://www.twitch.tv/" + name
	case "youtube":
		return "https://www.youtube.com/channel/" + id
	case "steam":
		return "https://steamcommunity.com/profiles/" + id
	case "spotify":
		return "https://open.spotify.com/user/" + id
	case "reddit":
		return "https://www.reddit.com/u/" + name
	case "tiktok":
		return "https://www.tiktok.com/@" + name
	case "instagram":
		return "https://www.instagram.com/" + name
	case "facebook":
		return "https://www.facebook.com/" + id
	case "roblox":
		return "https://www.roblox.com/users/" + id + "/profile"
	case "bluesky":
		return "https://bsky.app/profile/" + name
	case "ebay":
		return "https://www.ebay.com/usr/" + name
	case "domain":
		return "https://" + a.Name
	}
	return ""
}

// profileGames is a widget of games of a profile, and widgetGame one of them.
type profileGames struct {
	title string
	games []widgetGame
}

type widgetGame struct {
	AppID   discord.AppID `json:"application_id"`
	Comment string        `json:"comment"`
}

// widgetTitles are the game widgets shown, by their kind, in this order.
var widgetTitles = []struct{ kind, title string }{
	{"favorite_games", "Favorite Games"},
	{"current_games", "Playing"},
	{"played_games", "Played"},
	{"want_to_play_games", "Wants to Play"},
}

// gameWidgets reads the game widgets out of a profile's widgets. Discord
// sends each as {"data": {"type": ..., "games": [...]}}; one it sends in
// another shape, or of another kind, is left out.
func gameWidgets(raw []json.RawMessage) []profileGames {
	byKind := make(map[string][]widgetGame)
	for _, r := range raw {
		var w struct {
			Type  string       `json:"type"`
			Games []widgetGame `json:"games"`
			Data  *struct {
				Type  string       `json:"type"`
				Games []widgetGame `json:"games"`
			} `json:"data"`
		}
		if json.Unmarshal(r, &w) != nil {
			continue
		}
		kind, games := w.Type, w.Games
		if w.Data != nil {
			kind, games = w.Data.Type, w.Data.Games
		}
		for _, g := range games {
			if g.AppID.IsValid() {
				byKind[kind] = append(byKind[kind], g)
			}
		}
	}
	var out []profileGames
	for _, t := range widgetTitles {
		if g := byKind[t.kind]; len(g) > 0 {
			out = append(out, profileGames{title: t.title, games: g})
		}
	}
	return out
}

// appInfos keeps the names and icons of applications, asked for once each.
var appInfos sync.Map // discord.AppID → *appInfo

type appInfo struct {
	once       sync.Once
	name, icon string
	done       atomic.Bool
}

// appInfo is the name and icon of the application id, empty until they
// arrive.
func (c *chatView) appInfo(id discord.AppID) (name, icon string) {
	if c.demo || !id.IsValid() {
		return "", ""
	}
	v, _ := appInfos.LoadOrStore(id, new(appInfo))
	a := v.(*appInfo)
	a.once.Do(func() {
		go func() {
			info, err := c.st.Client.RequestJSON[struct {
				Name string `json:"name"`
				Icon string `json:"icon"`
			}]("GET", api.Endpoint+"applications/"+id.String()+"/rpc")
			if err == nil {
				a.name, a.icon = info.Name, info.Icon
			}
			a.done.Store(true)
			c.u.win.Invalidate()
		}()
	})
	if !a.done.Load() {
		return "", ""
	}
	if a.icon != "" {
		icon = "https://cdn.discordapp.com/app-icons/" + id.String() + "/" + a.icon + ".png?size=64"
	}
	return a.name, icon
}

// noteProfileExtras says in the log what kinds of widgets a profile came
// with, to tell what Discord sends when one does not show.
func noteProfileExtras(d *profileData) {
	if len(d.Widgets) == 0 {
		return
	}
	var kinds []string
	for _, r := range d.Widgets {
		var w struct {
			Type string `json:"type"`
			Data *struct {
				Type string `json:"type"`
			} `json:"data"`
		}
		json.Unmarshal(r, &w)
		if w.Data != nil {
			w.Type = w.Data.Type
		}
		kinds = append(kinds, w.Type)
	}
	slog.Info("profile widgets", "kinds", kinds, "games", len(gameWidgets(d.Widgets)))
}

// iconOpenLink marks what opens a page in the browser.
var iconOpenLink = mustIcon(icons.ActionOpenInNew)

//go:embed connicons/*.png
var connIconFiles embed.FS

// connIcons are the marks of the services, read once each. They come from
// Simple Icons (CC0), in each brand's color, or white where that is too
// dark to see on a card.
var connIcons sync.Map // kind → *paint.ImageOp, nil if there is none

// connIcon is the mark of the service kind, or nil.
func connIcon(kind string) *paint.ImageOp {
	if v, ok := connIcons.Load(kind); ok {
		op, _ := v.(*paint.ImageOp)
		return op
	}
	var op *paint.ImageOp
	if data, err := connIconFiles.ReadFile("connicons/" + kind + ".png"); err == nil {
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			o := paint.NewImageOp(img)
			op = &o
		}
	}
	connIcons.Store(kind, op)
	return op
}

// layoutConnections are the accounts linked to the profile, each a line as
// Discord draws them: the service's mark, the account's name over the
// service's, a check if it is verified, and an arrow if it opens a page.
func (c *chatView) layoutConnections(gtx layout.Context, p *profileView, accounts []connectedAccount) layout.Dimensions {
	th := c.u.th
	if p.linkClicks == nil {
		p.linkClicks = make(map[string]*widget.Clickable)
	}
	rows := []layout.FlexChild{
		layout.Rigid(bold(label(th, 12, colorText, "Connections")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
	}
	for _, a := range accounts {
		link := connectionURL(a)
		cl := clickOf(p.linkClicks, a.Type+"/"+a.ID)
		if link != "" && cl.Clicked(gtx) {
			openLink(link)
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			row := func(gtx layout.Context) layout.Dimensions {
				bg := withAlpha(colorMenu, 0xb0)
				if link != "" && cl.Hovered() {
					bg = colorHover
				}
				return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(7), Bottom: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								s := gtx.Dp(22)
								size := image.Pt(s, s)
								if img := connIcon(a.Type); img != nil {
									gtx.Constraints = layout.Exact(size)
									return widget.Image{Src: *img, Fit: widget.Contain}.Layout(gtx)
								}
								// A service without a mark: its initial.
								return avatar(gtx, th, 22, colorSecondaryButton, connectionName(a.Type), gtx.Dp(5))
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
											layout.Rigid(func(gtx layout.Context) layout.Dimensions {
												gtx.Constraints.Max.X -= gtx.Dp(20)
												return oneLine(bold(label(th, 14, colorHeader, a.Name))).Layout(gtx)
											}),
											layout.Rigid(func(gtx layout.Context) layout.Dimensions {
												if !a.Verified {
													return layout.Dimensions{}
												}
												return layout.Inset{Left: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
													return icon(gtx, iconCheck, 14, colorGreen)
												})
											}),
										)
									}),
									layout.Rigid(oneLine(label(th, 11, colorMuted, connectionName(a.Type))).Layout),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if link == "" {
									return layout.Dimensions{}
								}
								return icon(gtx, iconOpenLink, 16, colorMuted)
							}),
						)
					})
				})
			}
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if link == "" {
					return row(gtx)
				}
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return row(gtx)
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutProfileGames is a widget of games: its title, and each game with
// its icon, its name and what the user wrote of it.
func (c *chatView) layoutProfileGames(gtx layout.Context, w profileGames) layout.Dimensions {
	th := c.u.th
	rows := []layout.FlexChild{
		layout.Rigid(bold(label(th, 12, colorText, w.title)).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
	}
	for _, g := range w.games[:min(len(w.games), 12)] {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			name, icon := c.appInfo(g.AppID)
			if name == "" {
				name = "…"
			}
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return c.u.picture(gtx, icon, 28, colorPanel, name, 6)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lines := []layout.FlexChild{layout.Rigid(oneLine(label(th, 13, colorText, name)).Layout)}
						if g.Comment != "" {
							l := label(th, 12, colorMuted, g.Comment)
							l.MaxLines = 2
							lines = append(lines, layout.Rigid(l.Layout))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// selfMenuItems is the menu of the user panel: status, Performance Mode,
// the user's ID, quitting and logging out.
func (c *chatView) selfMenuItems() []menuItem {
	status := c.status(discord.NullGuildID, c.me.ID)
	items := []menuItem{{title: true, text: "Status"}}
	for _, choice := range statusChoices {
		s := choice.status
		items = append(items, menuItem{key: "status" + string(s), text: statusName(s), check: checked(status == s), act: func() { c.setStatus(s) }})
	}
	items = append(items,
		menuItem{sep: true},
		menuItem{key: "performance", text: "Performance Mode", check: checked(c.u.images.lowPower), act: func() {
			if err := c.u.setPerformanceMode(!c.u.images.lowPower); err != nil {
				slog.Warn("could not save performance mode", "err", err)
			}
		}},
		menuItem{key: "copyid", text: "Copy user ID", lead: menuIcon(iconCopy, colorMuted), act: func() { c.copyText = c.me.ID.String() }},
		menuItem{sep: true},
		menuItem{key: "quit", text: "Quit " + appName, lead: menuIcon(iconQuit, colorMuted), act: func() { c.u.quit() }},
		menuItem{key: "logout", text: "Log out", col: colorRed, lead: menuIcon(iconLogout, colorRed), act: func() { c.u.logout("") }},
	)
	return items
}
