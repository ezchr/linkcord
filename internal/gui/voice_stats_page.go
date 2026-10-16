package gui

import (
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
)

// voiceStatsRows are how long the user spent in voice, in Linkcord
// Features: in all, in which channels and with whom.
func (c *chatView) voiceStatsRows(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	g := &c.settings.general
	s := c.stats
	if c.u.app.NoVoiceStats || s == nil {
		return layout.Dimensions{}
	}
	if g.statsResetBtn.Clicked(gtx) {
		*s = voiceStats{Since: time.Now(), Channels: map[string]*voiceStatRow{}, People: map[string]*voiceStatRow{}}
		s.save()
	}
	row := func(name, value string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, oneLine(label(th, 14, colorText, name)).Layout),
				layout.Rigid(label(th, 14, colorMuted, value).Layout),
			)
		}
	}
	rows := []layout.Widget{
		layout.Spacer{Height: unit.Dp(12)}.Layout,
		bold(label(th, 12, colorText, "IN VOICE SINCE "+formatDate(s.Since))).Layout,
		row(tr("Total"), formatHours(s.Total)),
	}
	if top := topStats(s.Channels, 5); len(top) > 0 {
		rows = append(rows, layout.Spacer{Height: unit.Dp(10)}.Layout, bold(label(th, 12, colorText, "CHANNELS")).Layout)
		for _, r := range top {
			rows = append(rows, row(r.Name, formatHours(r.Time)))
		}
	}
	if top := topStats(s.People, 10); len(top) > 0 {
		rows = append(rows, layout.Spacer{Height: unit.Dp(10)}.Layout, bold(label(th, 12, colorText, "PEOPLE")).Layout)
		for _, r := range top {
			rows = append(rows, row(r.Name, formatHours(r.Time)))
		}
	}
	rows = append(rows, layout.Spacer{Height: unit.Dp(10)}.Layout, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return button(gtx, th, &g.statsResetBtn, colorSecondaryButton, tr("Start Again"))
		}))
	})
	return stacked(rows...)(gtx)
}
