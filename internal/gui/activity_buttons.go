package gui

import (
	"fmt"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// activityButtonKey names a button of an activity, for its click.
type activityButtonKey struct {
	user  discord.UserID
	app   discord.AppID
	label string
}

// layoutActivityButtons are the buttons of an activity, such as "Watch" or
// "Join", under it, as on Discord. Their links come with the activity, or
// are asked of Discord when clicked, as its app does for other people.
func (c *chatView) layoutActivityButtons(gtx layout.Context, a discord.Activity, userID discord.UserID) layout.Dimensions {
	if len(a.Buttons) == 0 {
		return layout.Dimensions{}
	}
	if c.activityBtns == nil {
		c.activityBtns = make(map[activityButtonKey]*widget.Clickable)
	}
	th := c.u.th
	var rows []layout.FlexChild
	for i, text := range a.Buttons {
		cl := clickOf(c.activityBtns, activityButtonKey{userID, a.AppID, text})
		if cl.Clicked(gtx) {
			c.openActivityButton(a, userID, i)
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					bg := colorSecondaryButton
					if cl.Hovered() {
						bg = withAlpha(bg, 0xd0)
					}
					return rounded(gtx, bg, 6, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Center.Layout(gtx, oneLine(bold(label(th, 14, colorHeader, text))).Layout)
						})
					})
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// openActivityButton opens where button i of the activity leads.
func (c *chatView) openActivityButton(a discord.Activity, userID discord.UserID, i int) {
	if m := a.Metadata; m != nil && i < len(m.ButtonURLs) && m.ButtonURLs[i] != "" {
		openLink(m.ButtonURLs[i])
		return
	}
	if c.demo || a.SessionID == "" || !a.AppID.IsValid() {
		c.showNotice("This button has no link")
		return
	}
	go func() {
		endpoint := fmt.Sprintf("%s%s/sessions/%s/activities/%s/metadata", api.EndpointUsers, userID, a.SessionID, a.AppID)
		meta, err := c.st.Client.RequestJSON[discord.ActivityMetadata]("GET", endpoint)
		c.u.post(func() {
			if err != nil || i >= len(meta.ButtonURLs) || meta.ButtonURLs[i] == "" {
				c.showNotice("Could not open the button")
				return
			}
			openLink(meta.ButtonURLs[i])
		})
	}()
}
