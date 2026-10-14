package gui

import (
	"fmt"
	"image"
	"time"

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
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// guildProfile is what Discord tells of a server whose tag someone wears.
type guildProfile struct {
	Name        string `json:"name"`
	IconHash    string `json:"icon_hash"`
	BannerHash  string `json:"custom_banner_hash"`
	Description string `json:"description"`
	MemberCount uint64 `json:"member_count"`
	OnlineCount uint64 `json:"online_count"`
	BrandColor  string `json:"brand_color_primary"`
}

// tagCard is the card a click on a server tag opens, as on Discord.
type tagCard struct {
	guildID         discord.GuildID
	tag, badge      string
	profile         *guildProfile
	err             string
	adopting        bool
	backdrop, adopt widget.Clickable
}

// openTagCard shows the card of the server of a tag.
func (c *chatView) openTagCard(guildID discord.GuildID, badge, tag string) {
	card := &tagCard{guildID: guildID, tag: tag, badge: badge}
	c.tagCard = card
	// The tag sits on the name, whose click opens the profile: the click
	// was for the tag.
	if c.profile != nil && time.Since(c.profile.opened) < 300*time.Millisecond {
		c.profile = nil
	}
	if c.demo {
		return
	}
	go func() {
		p, err := c.st.Client.RequestJSON[guildProfile]("GET", api.EndpointGuilds+guildID.String()+"/profile")
		c.u.post(func() {
			if err != nil {
				card.err = "Could not load the server: " + err.Error()
				return
			}
			card.profile = &p
		})
	}()
}

func (c *chatView) layoutTagCard(gtx layout.Context) {
	t := c.tagCard
	th := c.u.th
	size := gtx.Constraints.Max
	if t.backdrop.Clicked(gtx) {
		c.tagCard = nil
		return
	}
	if t.adopt.Clicked(gtx) && !t.adopting {
		t.adopting = true
		id := t.guildID
		go func() {
			err := c.st.Client.FastRequest("PUT", api.EndpointMe+"/clan",
				httputil.WithJSONBody(map[string]any{"identity_guild_id": id.String(), "identity_enabled": true}))
			c.u.post(func() {
				t.adopting = false
				if err != nil {
					c.showNotice("Could not adopt the tag: " + err.Error())
					return
				}
				c.showNotice("Tag adopted")
				c.tagCard = nil
			})
		}()
	}
	t.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, withAlpha(rgb(0x000000), 0x99), clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})

	name, desc, icon, banner := "Server", "", "", ""
	var online, members uint64
	brand := colorAccent
	g, gerr := c.st.Cabinet.Guild(t.guildID)
	member := gerr == nil
	if member {
		name, desc = g.Name, g.Description
		icon = cdnSize(g.IconURLWithType(discord.PNGImage))
		if g.Banner != "" {
			banner = "https://cdn.discordapp.com/banners/" + t.guildID.String() + "/" + string(g.Banner) + ".png?size=480"
		}
	}
	if p := t.profile; p != nil {
		name = p.Name
		if p.Description != "" {
			desc = p.Description
		}
		online, members = p.OnlineCount, p.MemberCount
		if p.IconHash != "" {
			icon = "https://cdn.discordapp.com/icons/" + t.guildID.String() + "/" + p.IconHash + ".png?size=128"
		}
		if p.BannerHash != "" {
			banner = "https://cdn.discordapp.com/banners/" + t.guildID.String() + "/" + p.BannerHash + ".png?size=480"
		}
		var col uint32
		if _, err := fmt.Sscanf(p.BrandColor, "#%06x", &col); err == nil {
			brand = rgb(col)
		}
	}
	wearing := c.me.PrimaryGuild != nil && c.me.PrimaryGuild.Enabled && c.me.PrimaryGuild.GuildID == t.guildID

	cardW := min(gtx.Dp(320), size.X-gtx.Dp(24))
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(cardW, 0), Max: image.Pt(cardW, size.Y-gtx.Dp(24))}
	macro := op.Record(gtx.Ops)
	dims := rounded(cgtx, colorPopup, 10, func(gtx layout.Context) layout.Dimensions {
		bannerH := gtx.Dp(110)
		iconPx := gtx.Dp(72)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := bannerH + iconPx/2
				r := image.Rect(0, 0, cardW, bannerH)
				paint.FillShape(gtx.Ops, brand, clip.UniformRRect(r, gtx.Dp(10)).Op(gtx.Ops))
				if banner != "" {
					if img := c.u.images.frame(gtx, banner, cardW); img != nil {
						bgtx := gtx
						bgtx.Constraints = layout.Exact(r.Max)
						st := clip.UniformRRect(r, gtx.Dp(10)).Push(gtx.Ops)
						widget.Image{Src: *img, Fit: widget.Cover}.Layout(bgtx)
						st.Pop()
					}
				}
				x, y := gtx.Dp(16), bannerH-iconPx/2
				ring := gtx.Dp(4)
				paint.FillShape(gtx.Ops, colorPopup, clip.UniformRRect(image.Rect(x-ring, y-ring, x+iconPx+ring, y+iconPx+ring), gtx.Dp(20)).Op(gtx.Ops))
				st := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
				c.u.picture(gtx, icon, unit.Dp(float32(iconPx)/gtx.Metric.PxPerDp), colorSidebar, name, gtx.Dp(18))
				st.Pop()
				return layout.Dimensions{Size: image.Pt(cardW, h)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(10), Bottom: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					rows := []layout.FlexChild{
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(oneLine(bold(label(th, 18, colorHeader, name))).Layout),
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return c.guildTagChip(gtx, t.guildID, t.badge, t.tag)
								}))
						}),
					}
					if members > 0 {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return memberCounts(gtx, th, online, members)
							})
						}))
					}
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						est := t.guildID.Time().Local()
						return layout.Inset{Top: unit.Dp(2)}.Layout(gtx,
							label(th, 13, colorMuted, "Est. "+est.Format("Jan 2006")).Layout)
					}))
					if desc != "" {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, label(th, 13, colorText, desc).Layout)
						}))
					}
					if t.err != "" {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 12, colorMuted, t.err).Layout)
						}))
					}
					// A tag is adopted from a server the user is in.
					if member && !wearing && !c.demo {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return t.adopt.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									pointer.CursorPointer.Add(gtx.Ops)
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									text := "Adopt Tag"
									if t.adopting {
										text = "Adopting…"
									}
									return rounded(gtx, colorGreenButton, 6, func(gtx layout.Context) layout.Dimensions {
										return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return layout.Center.Layout(gtx, bold(label(th, 14, colorHeader, text)).Layout)
										})
									})
								})
							})
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				})
			}))
	})
	call := macro.Stop()
	pos := image.Pt((size.X-cardW)/2, (size.Y-dims.Size.Y)/2)
	st := op.Offset(pos).Push(gtx.Ops)
	// Clicks on the card stay on it, under its button.
	solid(gtx, &t.err, dims)
	call.Add(gtx.Ops)
	st.Pop()
}

// memberCounts is a green dot with the members online and a gray one with
// all the members.
func memberCounts(gtx layout.Context, th *material.Theme, online, total uint64) layout.Dimensions {
	dot := func(col colorT) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			s := gtx.Dp(8)
			paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(s, s)}
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(dot(colorGreen)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(5)}.Layout),
		layout.Rigid(oneLine(label(th, 13, colorMuted, groupThousands(online)+" Online")).Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(dot(colorMuted)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(5)}.Layout),
		layout.Rigid(oneLine(label(th, 13, colorMuted, groupThousands(total)+" Members")).Layout),
	)
}
