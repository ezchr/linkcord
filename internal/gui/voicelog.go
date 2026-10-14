package gui

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconHistory = mustIcon(icons.ActionHistory)

// voiceLogEntry is someone joining or leaving the voice channel the user
// is in.
type voiceLogEntry struct {
	user   discord.UserID
	joined bool
	at     time.Time
}

// voiceLog is who joined and left the voice channel since the user came
// in, newest last, and the button and tab that show it.
type voiceLog struct {
	entries []voiceLogEntry
	open    bool
	btn     widget.Clickable
	list    widget.List
	// panelH and panelW are the size of the user panel it opens above.
	panelH, panelW int
}

// noteVoiceLog adds someone joining or leaving to the history.
func (c *chatView) noteVoiceLog(user discord.UserID, joined bool) {
	l := &c.voiceLog
	l.entries = append(l.entries, voiceLogEntry{user: user, joined: joined, at: time.Now()})
	if len(l.entries) > 200 {
		l.entries = l.entries[len(l.entries)-200:]
	}
}

// layoutVoiceLogButton is the button by the voice buttons that opens the
// history, while the user is in a voice channel.
func (c *chatView) layoutVoiceLogButton(gtx layout.Context) layout.Dimensions {
	if !c.u.voiceStatus().Joined {
		c.voiceLog.open = false
		return layout.Dimensions{}
	}
	l := &c.voiceLog
	if l.btn.Clicked(gtx) {
		l.open = !l.open
	}
	col := colorMuted
	if l.open {
		col = colorHeader
	}
	return iconButton(gtx, &l.btn, iconHistory, col, colorHeader)
}

// layoutVoiceLog is the tab of the history, over the bottom of the
// channels, as wide as them.
func (c *chatView) layoutVoiceLog(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	l := &c.voiceLog
	l.list.Axis = layout.Vertical
	status := c.u.voiceStatus()
	var guildID discord.GuildID
	if ch, err := c.st.Cabinet.Channel(status.ChannelID); err == nil {
		guildID = ch.GuildID
	}
	// A thin edge of the separators' color, to stand out on dark themes.
	return rounded(gtx, colorSelected, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(1)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return rounded(gtx, colorPanel, 8, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(bold(label(th, 12, colorMuted, "VOICE HISTORY")).Layout),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if len(l.entries) == 0 {
								return label(th, 13, colorMuted, "No one has joined or left yet.").Layout(gtx)
							}
							n := len(l.entries)
							return listLayout(gtx, th, &l.list, n, func(gtx layout.Context, i int) layout.Dimensions {
								// Newest first.
								e := l.entries[n-1-i]
								verb, col := "left", colorRed
								if e.joined {
									verb, col = "joined", colorGreen
								}
								name := c.memberName(guildID, e.user, nil)
								return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											s := gtx.Dp(8)
											paint.FillShape(gtx.Ops, col, clip.Ellipse(image.Rect(0, 0, s, s)).Op(gtx.Ops))
											return layout.Dimensions{Size: image.Pt(s, s)}
										}),
										layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
										layout.Flexed(1, oneLine(label(th, 13, colorText, name+" "+verb)).Layout),
										layout.Rigid(label(th, 12, colorMuted, e.at.Local().Format("15:04")).Layout),
									)
								})
							})
						}),
					)
				})
			})
		})
	})
}
