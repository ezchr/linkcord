package gui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconStreaming = mustIcon(icons.CommunicationScreenShare)

// voiceBadge is the badge at the top right of a server where people are in
// voice: a speaker, or a screen when one of them streams, as on Discord. It
// is nil where nobody is.
func (c *chatView) voiceBadge(guildID discord.GuildID) layout.Widget {
	states, err := c.st.Cabinet.VoiceStates(guildID)
	if err != nil || len(states) == 0 {
		return nil
	}
	ic := iconVoice
	for _, vs := range states {
		if vs.SelfStream {
			ic = iconStreaming
			break
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		// White on black, ringed in the column's color, as on Discord.
		return rounded(gtx, colorServers, 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, rgb(0x000000), 6, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(3)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, ic, 13, colorHeader)
					})
				})
			})
		})
	}
}
