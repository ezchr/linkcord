package gui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// guildCount is how many of a server's members are online, and in all.
type guildCount struct {
	online, total uint64
	at            time.Time
	fetching      bool
}

// layoutGuildCounts is the line under the server's name, in its button: a
// green dot with
// the members online, a gray one with all of them, as on Discord's invites.
func (c *chatView) layoutGuildCounts(gtx layout.Context, id discord.GuildID) layout.Dimensions {
	if c.guildCounts == nil {
		c.guildCounts = make(map[discord.GuildID]*guildCount)
	}
	n := c.guildCounts[id]
	if n == nil {
		n = &guildCount{}
		c.guildCounts[id] = n
	}
	// The demo has its few members.
	if c.demo {
		n.online, n.total, n.at = 5, 6, time.Now()
	}
	// Asked again every five minutes, never while drawing.
	if !n.fetching && time.Since(n.at) > 5*time.Minute {
		n.fetching = true
		go func() {
			g, err := c.st.Client.RequestJSON[discord.Guild]("GET", api.EndpointGuilds+id.String()+"?with_counts=true")
			c.u.post(func() {
				n.fetching, n.at = false, time.Now()
				if err == nil {
					n.online, n.total = g.ApproximatePresences, g.ApproximateMembers
				}
			})
		}()
	}
	if n.total == 0 {
		return layout.Dimensions{}
	}
	return layoutMemberCounts(gtx, c.u.th, n.online, n.total)
}

// layoutMemberCounts is a green dot with the members online and a gray one
// with all of them.
func layoutMemberCounts(gtx layout.Context, th *material.Theme, online, total uint64) layout.Dimensions {
	dot := func(col colorT) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			s := gtx.Dp(7)
			paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(s, s)}
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(dot(colorGreen)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(oneLine(label(th, 11, colorMuted, fmt.Sprintf("%s Online", groupThousands(online)))).Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
		layout.Rigid(dot(colorMuted)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(oneLine(label(th, 11, colorMuted, fmt.Sprintf("%s Members", groupThousands(total)))).Layout),
	)
}

// groupThousands writes n with commas: 12,480.
func groupThousands(n uint64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
