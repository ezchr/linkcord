package gui

import (
	"encoding/base64"
	"errors"
	"image"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// inviteRe finds server invites in a message: discord.gg/code,
// discord.com/invite/code and the old discordapp.com links.
var inviteRe = regexp.MustCompile(`(?i)(?:discord\.gg|discord(?:app)?\.com/invite)/([a-z0-9-]{2,32})`)

// maxInviteCards is how many invites one message shows, as Discord does.
const maxInviteCards = 3

// inviteCard is an invite shown under a message: the server it leads to and
// what came of joining it.
type inviteCard struct {
	inv      *discord.Invite
	invalid  bool // unknown or expired
	fetched  bool
	joining  bool
	joined   bool
	failed   string
	join     widget.Clickable
	open     widget.Clickable
}

// inviteFetches asks Discord for one invite at a time: a channel of
// partnerships holds dozens, and a burst of them is rate limited.
var inviteFetches sync.Mutex

// messageInvites returns the codes of the invites in a message's text and
// its embeds, each once.
func messageInvites(m discord.Message) []string {
	texts := []string{m.Content}
	for _, e := range m.Embeds {
		texts = append(texts, e.Description, string(e.URL))
		for _, f := range e.Fields {
			texts = append(texts, f.Value)
		}
	}
	var codes []string
	seen := map[string]bool{}
	for _, t := range texts {
		for _, sm := range inviteRe.FindAllStringSubmatch(t, -1) {
			code := sm[1]
			if seen[code] {
				continue
			}
			seen[code] = true
			if codes = append(codes, code); len(codes) == maxInviteCards {
				return codes
			}
		}
	}
	return codes
}

// invite returns the card of an invite code, asking Discord for it the
// first time it shows.
func (c *chatView) invite(code string) *inviteCard {
	if c.invites == nil {
		c.invites = make(map[string]*inviteCard)
	}
	ic := c.invites[code]
	if ic != nil {
		return ic
	}
	ic = &inviteCard{}
	c.invites[code] = ic
	if c.demo {
		ic.inv = &discord.Invite{Code: code, Guild: &discord.Guild{Name: "Linkcord Demo Server"}, ApproximatePresences: 5, ApproximateMembers: 6}
		ic.fetched = true
		return ic
	}
	go func() {
		inviteFetches.Lock()
		inv, err := c.st.Client.InviteWithCounts(code)
		time.Sleep(300 * time.Millisecond)
		inviteFetches.Unlock()
		c.u.post(func() {
			ic.fetched = true
			var herr *httputil.HTTPError
			switch {
			case err == nil && inv.Guild != nil:
				ic.inv = inv
			case err == nil || errors.As(err, &herr) && herr.Status == http.StatusNotFound:
				ic.invalid = true
			default:
				// Asked again the next time it shows.
				delete(c.invites, code)
			}
		})
	}()
	return ic
}

// joinInvite joins the server of an invite. Discord's own client tells it
// where the invite was clicked; a chat link is "Markdown Link".
func (c *chatView) joinInvite(ic *inviteCard) {
	ic.joining, ic.failed = true, ""
	code := ic.inv.Code
	go func() {
		ctx := base64.StdEncoding.EncodeToString([]byte(`{"location":"Markdown Link"}`))
		_, err := c.st.Client.RequestJSON[api.JoinedInvite]("POST", api.EndpointInvites+code,
			httputil.WithJSONBody(struct{}{}),
			httputil.WithHeaders(http.Header{"X-Context-Properties": {ctx}}))
		c.u.post(func() {
			ic.joining = false
			if err != nil {
				ic.failed = joinError(err)
				return
			}
			ic.joined = true
		})
	}()
}

// joinError says why a join failed. Discord asks for a captcha when it
// distrusts a join, which only its own client can answer.
func joinError(err error) string {
	var herr *httputil.HTTPError
	if errors.As(err, &herr) && strings.Contains(string(herr.Body), "captcha") {
		return "Discord wants a captcha: open the invite in the browser"
	}
	return err.Error()
}

// inviteChildren are the invite cards of a message.
func (c *chatView) inviteChildren(m discord.Message) []layout.FlexChild {
	var children []layout.FlexChild
	for _, code := range messageInvites(m) {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.layoutInvite(gtx, code)
		}))
	}
	return children
}

// layoutInvite is an invite card, as Discord shows one: the server's icon,
// name and members, and a button to join it.
func (c *chatView) layoutInvite(gtx layout.Context, code string) layout.Dimensions {
	ic := c.invite(code)
	if !ic.fetched {
		return layout.Dimensions{}
	}
	th := c.u.th
	var g discord.Guild
	member := false
	if ic.inv != nil {
		g = *ic.inv.Guild
		_, err := c.st.Cabinet.Guild(g.ID)
		member = ic.joined || err == nil && g.ID.IsValid()
	}
	if ic.join.Clicked(gtx) {
		switch {
		case member:
			c.selectGuildIfNot(g.ID)
		case ic.inv != nil && !ic.joining:
			c.joinInvite(ic)
		}
	}
	if ic.open.Clicked(gtx) {
		openLink("https://discord.gg/" + code)
	}
	heading := "YOU'VE BEEN INVITED TO JOIN A SERVER"
	if ic.invalid {
		heading = "YOU RECEIVED AN INVITE, BUT..."
	}
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(432))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(bold(label(th, 12, colorMuted, heading)).Layout),
					layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if ic.invalid {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return c.u.picture(gtx, "", 50, colorPanel, "?", -1)
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
								layout.Flexed(1, bold(label(th, 16, colorRed, "Invalid Invite")).Layout),
							)
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return c.u.picture(gtx, cdnSize(g.IconURLWithType(discord.PNGImage)), 50, colorFor(uint64(g.ID)), g.Name, gtx.Dp(16))
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return ic.open.Layout(gtx, oneLine(bold(label(th, 16, colorHeader, g.Name))).Layout)
									}),
									layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layoutMemberCounts(gtx, th, uint64(ic.inv.ApproximatePresences), uint64(ic.inv.ApproximateMembers))
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if ic.failed == "" {
											return layout.Dimensions{}
										}
										return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, label(th, 12, colorRed, ic.failed).Layout)
									}),
								)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								txt, bg := "Join", colorGreen
								switch {
								case member:
									txt, bg = "Joined", colorPanel
								case ic.joining:
									txt, bg = "Joining...", darker(colorGreen)
								}
								gtx.Constraints.Min = image.Pt(gtx.Dp(80), gtx.Dp(40))
								return button(gtx, th, &ic.join, bg, tr(txt))
							}),
						)
					}),
				)
			})
		})
	})
}
