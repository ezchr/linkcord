package gui

import (
	"fmt"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// What Discord shows only to moderators shows to the user too, as Vencord's
// ShowHiddenThings and ShowTimeoutDuration do: who is timed out, and for
// how long, beside their name and in their profile.

var iconTimeout = mustIcon(icons.ImageTimer)

// timeoutUntil is when the timeout of id in the server ends, or zero when
// they are not timed out or the user chose not to see it.
func (c *chatView) timeoutUntil(guildID discord.GuildID, id discord.UserID) time.Time {
	if c.u.app.NoModInfo || !guildID.IsValid() {
		return time.Time{}
	}
	m, err := c.st.Cabinet.Member(guildID, id)
	if err != nil {
		return time.Time{}
	}
	return memberTimeout(m)
}

// memberTimeout is when m's timeout ends, or zero.
func memberTimeout(m *discord.Member) time.Time {
	if m == nil || !m.CommunicationDisabledUntil.IsValid() {
		return time.Time{}
	}
	if t := m.CommunicationDisabledUntil.Time(); t.After(time.Now()) {
		return t
	}
	return time.Time{}
}

// formatLeft is how long until t, roughly.
func formatLeft(t time.Time) string {
	d := time.Until(t)
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d d", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d min", max(1, int(d.Minutes()+0.5)))
	}
}

// layoutTimeoutMark is a clock and how long a timeout has left, beside a
// name, or nothing.
func (c *chatView) layoutTimeoutMark(gtx layout.Context, until time.Time) layout.Dimensions {
	if until.IsZero() {
		return layout.Dimensions{}
	}
	// Redrawn as the minutes go.
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Minute)})
	return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconTimeout, 14, colorRed) }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(2)}.Layout),
			layout.Rigid(label(c.u.th, 12, colorRed, formatLeft(until)).Layout),
		)
	})
}
