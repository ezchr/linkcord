package gui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// guildCount is how many of a server's members are online, and in all.
type guildCount struct {
	online, total uint64
	at            time.Time
	fetching      bool
}

// layoutGuildCounts is the line under the server's name: a green dot with
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
	th := c.u.th
	dot := func(col colorT) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			s := gtx.Dp(8)
			paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(s, s)}
		}
	}
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(dot(colorGreen)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(5)}.Layout),
			layout.Rigid(oneLine(label(th, 12, colorMuted, fmt.Sprintf("%s Online", groupThousands(n.online)))).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
			layout.Rigid(dot(colorMuted)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(5)}.Layout),
			layout.Rigid(oneLine(label(th, 12, colorMuted, fmt.Sprintf("%s Members", groupThousands(n.total)))).Layout),
		)
	})
}

// groupThousands writes n with commas: 12,480.
func groupThousands(n uint64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
