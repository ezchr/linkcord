package gui

import (
	"cmp"
	"slices"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Activities the user hides are left out of their status: games detected
// and what programs show over the RPC, each by its application.

// shownActivity is a game or program that shows, or would, on the status.
type shownActivity struct {
	id, name string
}

// activityHidden reports whether the application id is kept out of the
// user's status.
func (c *chatView) activityHidden(id string) bool {
	_, ok := c.u.app.HiddenActivities[id]
	return ok
}

// knownActivities are the games and programs showing now, or hidden, by
// name.
func (c *chatView) knownActivities() []shownActivity {
	seen := make(map[string]bool)
	var out []shownActivity
	add := func(id, name string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, shownActivity{id, cmp.Or(name, tr("Application")+" "+id)})
	}
	for _, p := range c.games {
		add(p.game.ID, p.game.Name)
	}
	for _, a := range c.rpcActivities {
		name := ""
		if v, ok := rpcApps.Load(a.clientID); ok {
			name = v.(*rpcApp).loadedName()
		}
		add(a.clientID, name)
	}
	for id, name := range c.u.app.HiddenActivities {
		add(id, name)
	}
	slices.SortFunc(out, func(a, b shownActivity) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
	return out
}

// updateHiddenActivities turns the activities the user clicked.
func (c *chatView) updateHiddenActivities(gtx layout.Context) {
	g := &c.settings.general
	if g.activityBtns == nil {
		g.activityBtns = make(map[string]*widget.Clickable)
	}
	for _, a := range c.knownActivities() {
		if !clickOf(g.activityBtns, a.id).Clicked(gtx) {
			continue
		}
		app := &c.u.app
		if c.activityHidden(a.id) {
			delete(app.HiddenActivities, a.id)
		} else {
			if app.HiddenActivities == nil {
				app.HiddenActivities = make(map[string]string)
			}
			app.HiddenActivities[a.id] = a.name
		}
		c.u.saveAppSettings()
		if !c.demo {
			c.pushPresence()
		}
	}
}

// hiddenActivityRows lists the games and programs showing or hidden, each
// with whether it shows on the status.
func (c *chatView) hiddenActivityRows(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	g := &c.settings.general
	if g.activityBtns == nil {
		g.activityBtns = make(map[string]*widget.Clickable)
	}
	rows := []layout.Widget{
		label(th, 16, colorHeader, "Activities on Your Status").Layout,
		label(th, 14, colorMuted, "Games and programs showing now, or hidden before: those turned off never show on your status.").Layout,
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	}
	acts := c.knownActivities()
	if len(acts) == 0 {
		rows = append(rows, label(th, 14, colorMuted, "Nothing is showing now.").Layout)
	}
	for _, a := range acts {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return switchRow(gtx, th, clickOf(g.activityBtns, a.id), a.name, "", !c.activityHidden(a.id))
			})
		})
	}
	return stacked(rows...)(gtx)
}
