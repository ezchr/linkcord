package gui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// Linkcord is the web client to Discord: its requests, headers and
// connection are a browser's, so the gateway is told the same. Showing
// another platform (a phone, a console, the desktop app) would have only the
// gateway say so while every other request still said Chrome on Windows, a
// mixed identity; the choice that did that is gone, and a platform chosen
// before is ignored.

// withPlatform is props as the session sends them: those of the web, as
// they are.
func withPlatform(props gateway.IdentifyProperties) gateway.IdentifyProperties {
	return props
}

// platformView was the choice of platform in the settings.
type platformView struct{}

func (c *chatView) updatePlatform(gtx layout.Context) {}

// platformRows say what platform the others see.
func (c *chatView) platformRows(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	return stacked(
		label(th, 16, colorHeader, "Platform Shown").Layout,
		label(th, 14, colorMuted, "Others see you connected from the web, as Linkcord talks to Discord as its web app does. Showing a phone, console or the desktop app is no longer offered: only part of the connection could say so, which Discord can tell apart.").Layout,
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	)(gtx)
}
