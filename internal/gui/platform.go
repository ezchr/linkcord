package gui

import (
	"sync/atomic"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// The platform Linkcord tells Discord it is, which the others see beside
// the user's status: a computer, a phone, the web or a console. Linkcord is
// the web, as its requests are a browser's.

// platformChoice is a platform to show, and what Discord's own app on it
// says when it connects.
type platformChoice struct {
	id, name            string
	browser, os, device string
}

var platformChoices = []platformChoice{
	{id: "", name: "Web (as Linkcord is)"},
	{id: "desktop", name: "Computer", browser: "Discord Client", os: "Windows"},
	{id: "android", name: "Android phone", browser: "Discord Android", os: "Android", device: "Android"},
	{id: "ios", name: "iPhone", browser: "Discord iOS", os: "iOS", device: "iPhone"},
	{id: "xbox", name: "Xbox", browser: "Discord Embedded", os: "Xbox"},
	{id: "playstation", name: "PlayStation", browser: "Discord Embedded", os: "Playstation"},
}

// identifyPlatform is the platform chosen, read as the gateway connects.
var identifyPlatform atomic.Value // string

// withPlatform is props saying they come from the platform chosen.
func withPlatform(props gateway.IdentifyProperties) gateway.IdentifyProperties {
	id, _ := identifyPlatform.Load().(string)
	for _, p := range platformChoices {
		if p.id != id || p.id == "" {
			continue
		}
		props[gateway.IdentifyBrowser] = p.browser
		props[gateway.IdentifyOS] = p.os
		props[gateway.IdentifyDevice] = p.device
		delete(props, "browser_user_agent")
		delete(props, "browser_version")
	}
	return props
}

// platformView is the choice of platform in the settings.
type platformView struct {
	btns map[string]*widget.Clickable
	// changed says the choice applies on the next start.
	changed bool
}

func (c *chatView) updatePlatform(gtx layout.Context) {
	pv := &c.settings.general.platform
	if pv.btns == nil {
		pv.btns = make(map[string]*widget.Clickable)
	}
	for _, p := range platformChoices {
		if clickOf(pv.btns, p.id).Clicked(gtx) && p.id != c.u.app.Platform {
			c.u.app.Platform = p.id
			c.u.saveAppSettings()
			identifyPlatform.Store(p.id)
			pv.changed = true
		}
	}
}

// platformRows are the choice of the platform the others see.
func (c *chatView) platformRows(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	pv := &c.settings.general.platform
	if pv.btns == nil {
		pv.btns = make(map[string]*widget.Clickable)
	}
	about := "What the others see you connected from, beside your status. It applies the next time Linkcord starts."
	col := colorMuted
	if pv.changed {
		about, col = "Restart Linkcord for the others to see it.", colorYellow
	}
	rows := []layout.Widget{
		label(th, 16, colorHeader, "Platform Shown").Layout,
		label(th, 14, col, about).Layout,
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	}
	for _, p := range platformChoices {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, clickOf(pv.btns, p.id), p.name, p.id == c.u.app.Platform)
		})
	}
	return stacked(rows...)(gtx)
}
