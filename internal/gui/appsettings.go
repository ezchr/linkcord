package gui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/discord"
)

// appSettings are how Linkcord looks and behaves, kept in app.json beside
// the log.
type appSettings struct {
	// Zoom is how big the whole interface is, 1 as designed; 0 is 1.
	Zoom float32 `json:"zoom,omitempty"`
	// ChatText is how big the text of the chat is, in pixels at 100%; 0 is
	// 16, as Discord's.
	ChatText float32 `json:"chat_text,omitempty"`
	// GroupGap is the space between groups of messages, in pixels; nil is
	// 16.
	GroupGap *float32 `json:"group_gap,omitempty"`
	// NoRoleColors shows names without the colors of roles.
	NoRoleColors bool `json:"no_role_colors,omitempty"`
	// StartMinimized starts in the notification area when Windows starts
	// Linkcord, and QuitOnClose quits on closing the window rather than
	// keep running there.
	StartMinimized bool `json:"start_minimized,omitempty"`
	QuitOnClose    bool `json:"quit_on_close,omitempty"`
	// HideMuted are the servers whose muted channels are hidden.
	HideMuted []discord.GuildID `json:"hide_muted,omitempty"`
	// SearchHistory are the last searches, the newest first.
	SearchHistory []string `json:"search_history,omitempty"`
}

func appSettingsPath() string {
	return filepath.Join(filepath.Dir(logPath()), "app.json")
}

func loadAppSettings() appSettings {
	var a appSettings
	if data, err := os.ReadFile(appSettingsPath()); err == nil {
		json.Unmarshal(data, &a)
	}
	return a
}

// saveAppSettings keeps the settings, unless in the demo.
func (u *ui) saveAppSettings() {
	if c := u.chat; c != nil && c.demo {
		return
	}
	data, err := json.Marshal(u.app)
	if err != nil {
		return
	}
	go func() {
		if err := os.WriteFile(appSettingsPath(), data, 0o600); err != nil {
			slog.Warn("could not save the settings", "err", err)
		}
	}()
}

func (a appSettings) zoom() float32 {
	if a.Zoom <= 0 {
		return 1
	}
	return a.Zoom
}

func (a appSettings) chatScale() float32 {
	if a.ChatText <= 0 {
		return 1
	}
	return a.ChatText / 16
}

func (a appSettings) groupGap() unit.Dp {
	if a.GroupGap == nil {
		return 16
	}
	return unit.Dp(*a.GroupGap)
}

// zoomSteps are the zooms Ctrl+plus and Ctrl+minus go through, as Discord's.
var zoomSteps = []float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2}

// applyZoom makes the interface as big as chosen, and has Ctrl+plus,
// Ctrl+minus and Ctrl+0 change it, from anywhere.
func (u *ui) applyZoom(gtx *layout.Context) {
	for {
		e, ok := gtx.Event(
			key.Filter{Name: "+", Required: key.ModShortcut},
			key.Filter{Name: "=", Required: key.ModShortcut},
			key.Filter{Name: "-", Required: key.ModShortcut},
			key.Filter{Name: "0", Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		ev, ok := e.(key.Event)
		if !ok || ev.State != key.Press {
			continue
		}
		z := u.app.zoom()
		switch ev.Name {
		case "+", "=":
			for _, s := range zoomSteps {
				if s > z+0.001 {
					z = s
					break
				}
			}
		case "-":
			for i := len(zoomSteps) - 1; i >= 0; i-- {
				if zoomSteps[i] < z-0.001 {
					z = zoomSteps[i]
					break
				}
			}
		case "0":
			z = 1
		}
		u.app.Zoom = z
		u.saveAppSettings()
	}
	z := u.app.zoom()
	gtx.Metric.PxPerDp *= z
	gtx.Metric.PxPerSp *= z
}

// generalView is the state of the General and Accessibility pages.
type generalView struct {
	zoom, chatText, groupGap slider
	roleColors               widget.Clickable
	startup, minimized, tray widget.Clickable
	startupErr               string
}

func (c *chatView) updateGeneral(gtx layout.Context) {
	u := c.u
	g := &c.settings.general
	if r := g.zoom.update(gtx); r.released {
		// Changed when let go, as the slider itself grows with it.
		u.app.Zoom = snapStep(0.5+r.value*1.5, zoomSteps)
		u.saveAppSettings()
	}
	if r := g.chatText.update(gtx); r.changed || r.released {
		u.app.ChatText = float32(int(12 + r.value*12 + 0.5))
		if r.released {
			u.saveAppSettings()
		}
	}
	if r := g.groupGap.update(gtx); r.changed || r.released {
		v := float32(int(r.value*24 + 0.5))
		u.app.GroupGap = &v
		if r.released {
			u.saveAppSettings()
		}
	}
	if g.roleColors.Clicked(gtx) {
		u.app.NoRoleColors = !u.app.NoRoleColors
		u.saveAppSettings()
	}
	if g.startup.Clicked(gtx) && !c.demo {
		on := !startsWithWindows()
		if err := setStartWithWindows(on, u.app.StartMinimized); err != nil {
			g.startupErr = "Could not change it: " + err.Error()
		} else {
			g.startupErr = ""
		}
	}
	if g.minimized.Clicked(gtx) {
		u.app.StartMinimized = !u.app.StartMinimized
		u.saveAppSettings()
		if startsWithWindows() && !c.demo {
			setStartWithWindows(true, u.app.StartMinimized)
		}
	}
	if g.tray.Clicked(gtx) {
		u.app.QuitOnClose = !u.app.QuitOnClose
		u.saveAppSettings()
	}
}

// snapStep is the step of steps closest to v.
func snapStep(v float32, steps []float32) float32 {
	best := steps[0]
	for _, s := range steps {
		if abs32(s-v) < abs32(best-v) {
			best = s
		}
	}
	return best
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// sliderRow is a setting set on a slider: its name, what it does, the
// slider and its value.
func sliderRow(th *material.Theme, name, about string, s *slider, value float32, shown string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(18)}.Layout(gtx, stacked(
			label(th, 16, colorHeader, name).Layout,
			label(th, 14, colorMuted, about).Layout,
			layout.Spacer{Height: unit.Dp(6)}.Layout,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return s.layout(gtx, 0, value) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(64)
						return layout.E.Layout(gtx, label(th, 14, colorText, shown).Layout)
					}),
				)
			},
		))
	}
}

// accessibilityRows are the Accessibility page.
func (c *chatView) accessibilityRows() []layout.Widget {
	u := c.u
	th := u.th
	g := &c.settings.general
	z := u.app.zoom()
	if g.zoom.dragging {
		z = snapStep(0.5+g.zoom.value*1.5, zoomSteps)
	}
	text := u.app.chatScale() * 16
	gap := float32(u.app.groupGap())
	return []layout.Widget{
		sliderRow(th, "Zoom Level", "How big the whole of Linkcord is. Ctrl+plus, Ctrl+minus and Ctrl+0 change it too.", &g.zoom, (z-0.5)/1.5, fmt.Sprintf("%.0f%%", z*100)),
		sliderRow(th, "Chat Font Size", "How big the text of the chat is.", &g.chatText, (text-12)/12, fmt.Sprintf("%.0fpx", text)),
		sliderRow(th, "Space Between Message Groups", "The space above each group of messages by one author.", &g.groupGap, gap/24, fmt.Sprintf("%.0fpx", gap)),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.roleColors, "Role Colors on Names", "Show names in the color of their highest colored role.", !u.app.NoRoleColors)
		},
	}
}

// generalRows are the General page.
func (c *chatView) generalRows() []layout.Widget {
	u := c.u
	th := u.th
	g := &c.settings.general
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.startup, "Open Linkcord When Windows Starts", "", startsWithWindows())
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.minimized, "Start Minimized", "Start in the notification area, out of the way, when Windows opens Linkcord.", u.app.StartMinimized)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.tray, "Minimize to the Notification Area", "Closing the window keeps Linkcord running, voice included, with its icon by the clock.", !u.app.QuitOnClose)
		},
	}
	if g.startupErr != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, g.startupErr).Layout)
		})
	}
	return rows
}
