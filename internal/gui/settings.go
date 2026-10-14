package gui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ezchr/linkcord/internal/dcsound"
	"github.com/ezchr/linkcord/internal/hotkey"
	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconSettings = mustIcon(icons.ActionSettings)

// settingsPage is a page of the settings.
type settingsPage int

const (
	pageAccount settingsPage = iota
	pageProfiles
	pageSocial
	pagePrivacy
	pageVoice
	pageKeybinds
	pageSounds
	pageAppearance
	pageAccessibility
	pageGeneral
	pageAdvanced
	settingsPages
)

var settingsPageNames = [settingsPages]string{
	"My Account", "Profiles", "Content & Social", "Data & Privacy",
	"Voice & Video", "Keybinds", "Sounds", "Appearance", "Accessibility",
	"Windows Settings", "Advanced",
}

// settingsView is the screen of the user's settings, over the whole window,
// as Discord's, opened by the gear beside the voice buttons.
type settingsView struct {
	open bool
	// focused reports whether the keyboard focus was taken from what is
	// under the settings, for Escape to close them.
	focused bool
	page    settingsPage
	pages   [settingsPages]widget.Clickable
	list    widget.List

	closeBtn, logout widget.Clickable

	// Voice & Video.
	inputBtn, outputBtn widget.Clickable
	inVol, outVol, sens slider
	// test captures the microphone while the page shows, to show how loud
	// it is, and plays it back while checkBtn says so.
	test    *voice.MicTest
	testErr string
	// level is the level shown, which falls slower than it rises.
	level          float64
	checkBtn       widget.Clickable
	vadBtn, pttBtn widget.Clickable
	nsBtn, agcBtn  widget.Clickable
	soundsBtn      widget.Clickable
	// nsStrength is how much of the noise is removed.
	nsStrength slider

	// Sounds: each one's switch, volume, and button to hear it.
	soundSwitch, soundHear map[string]*widget.Clickable
	soundVolume            map[string]*slider

	// Appearance.
	look appearanceView
	// The account's pages.
	acct accountView
	// General and Accessibility.
	general      generalView
	keybindsLink widget.Clickable

	// Keybinds: recording is the action whose keys are being recorded.
	record, clear map[string]*widget.Clickable
	recording     string

	// Advanced.
	perfBtn, helloBtn, reportsBtn widget.Clickable
	perfErr                       string
}

// openSettings shows the settings at their first page.
func (c *chatView) openSettings() {
	s := &c.settings
	if s.open {
		return
	}
	c.closeMenu()
	c.profile = nil
	s.open, s.focused = true, false
	s.list.Axis = layout.Vertical
	if s.record == nil {
		s.record = make(map[string]*widget.Clickable)
		s.clear = make(map[string]*widget.Clickable)
	}
	s.level = voice.SilentLevel
	c.u.loadDevices()
	c.loadAccount()
	c.showSettingsPage(pageAccount)
}

// closeSettings goes back to the chat, letting go of the microphone.
func (c *chatView) closeSettings() {
	s := &c.settings
	c.stopRecording()
	c.stopMicTest()
	s.open = false
	c.closeMenu()
}

func (c *chatView) showSettingsPage(p settingsPage) {
	s := &c.settings
	c.stopRecording()
	s.page = p
	s.list.Position = layout.Position{}
	if p == pageVoice {
		c.startMicTest()
	} else {
		c.stopMicTest()
	}
}

// startMicTest captures the microphone chosen, to show how loud it is.
func (c *chatView) startMicTest() {
	s := &c.settings
	if s.test != nil || c.demo {
		return
	}
	t, err := voice.StartMicTest(c.u.voiceSettings().Input)
	if err != nil {
		s.testErr = "Could not open the microphone: " + err.Error()
		return
	}
	s.test, s.testErr = t, ""
	c.u.applyVoiceSettings()
}

func (c *chatView) stopMicTest() {
	s := &c.settings
	if s.test != nil {
		s.test.Stop()
		s.test = nil
	}
	s.level = voice.SilentLevel
}

// restartMicTest captures the devices chosen now, playing back again if it
// was.
func (c *chatView) restartMicTest() {
	s := &c.settings
	if s.test == nil {
		return
	}
	echo := s.test.Echoing()
	c.stopMicTest()
	c.startMicTest()
	if echo {
		c.toggleEcho()
	}
}

// toggleEcho plays back what the microphone captures, or stops.
func (c *chatView) toggleEcho() {
	s := &c.settings
	if s.test == nil {
		c.startMicTest()
		if s.test == nil {
			return
		}
	}
	if err := s.test.Echo(c.u.voiceSettings().Output, !s.test.Echoing()); err != nil {
		s.testErr = "Could not play your voice back: " + err.Error()
	}
}

// startRecording records the next keys pressed as those of the action id.
func (c *chatView) startRecording(id string) {
	u, s := c.u, &c.settings
	c.stopRecording()
	s.recording = id
	if u.voice.keys == nil {
		u.applyKeybinds()
	}
	u.voice.keys.Record(func(keys hotkey.Combo) {
		u.post(func() {
			if s.recording != id {
				return
			}
			s.recording = ""
			settings := u.voiceSettings()
			if settings.Keybinds == nil {
				settings.Keybinds = make(map[string]hotkey.Combo)
			}
			settings.Keybinds[id] = keys
			u.saveVoiceSettings()
			u.applyKeybinds()
		})
	})
}

func (c *chatView) stopRecording() {
	s := &c.settings
	if s.recording == "" {
		return
	}
	s.recording = ""
	if k := c.u.voice.keys; k != nil {
		k.Record(nil)
	}
}

// snapVolume keeps vol from 0 to 2, at 1 when close to it, as Discord's
// sliders do.
func snapVolume(vol float32) float32 {
	vol = min(max(vol, 0), 2)
	if vol > 0.96 && vol < 1.04 {
		vol = 1
	}
	return vol
}

func (c *chatView) updateSettings(gtx layout.Context) {
	u, s := c.u, &c.settings
	settings := u.voiceSettings()
	if !s.focused {
		// What had the keyboard is under the settings now.
		gtx.Execute(key.FocusCmd{})
		s.focused = true
	}
	for {
		e, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		// Escape may be a key being recorded.
		if e, ok := e.(key.Event); ok && e.State == key.Press && s.recording == "" {
			c.closeSettings()
			return
		}
	}
	if s.closeBtn.Clicked(gtx) {
		c.closeSettings()
		return
	}
	if s.logout.Clicked(gtx) {
		c.closeSettings()
		u.logout("")
		return
	}
	for p := range settingsPages {
		if s.pages[p].Clicked(gtx) && s.page != p {
			c.showSettingsPage(p)
		}
	}

	// Voice & Video.
	if s.inputBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 300, func() []menuItem { return c.settingsDeviceItems(true) })
	}
	if s.outputBtn.Clicked(gtx) {
		c.openMenu(c.pointerAt, 300, func() []menuItem { return c.settingsDeviceItems(false) })
	}
	volume := func(sl *slider, set func(float32)) {
		if r := sl.update(gtx); r.changed || r.released {
			set(snapVolume(r.value * 2))
			u.applyVoiceSettings()
			if r.released {
				u.saveVoiceSettings()
			}
		}
	}
	volume(&s.inVol, func(v float32) { settings.InputVolume = &v })
	volume(&s.outVol, func(v float32) { settings.OutputVolume = &v })
	if r := s.sens.update(gtx); r.changed || r.released {
		db := float64(voice.SilentLevel) * float64(1-r.value)
		settings.Sensitivity = &db
		u.applyVoiceSettings()
		if r.released {
			u.saveVoiceSettings()
		}
	}
	if s.checkBtn.Clicked(gtx) {
		c.toggleEcho()
	}
	for click, ptt := range map[*widget.Clickable]bool{&s.vadBtn: false, &s.pttBtn: true} {
		if click.Clicked(gtx) && settings.PushToTalk != ptt {
			settings.PushToTalk = ptt
			u.applyVoiceSettings()
			u.saveVoiceSettings()
		}
	}
	if s.nsBtn.Clicked(gtx) {
		settings.NoNoiseSuppression = !settings.NoNoiseSuppression
		u.applyVoiceSettings()
		u.saveVoiceSettings()
	}
	if r := s.nsStrength.update(gtx); r.changed || r.released {
		v := min(max(r.value, 0), 1)
		settings.NoiseStrength = &v
		u.applyVoiceSettings()
		if r.released {
			u.saveVoiceSettings()
		}
	}
	if s.soundsBtn.Clicked(gtx) {
		settings.NoSounds = !settings.NoSounds
		u.saveVoiceSettings()
		u.playSound(dcsound.Unmute)
	}
	c.updateSoundSettings(gtx)
	c.updateAppearance(gtx)
	c.updateAccount(gtx)
	c.updateGeneral(gtx)
	if s.agcBtn.Clicked(gtx) {
		settings.NoAutoGain = !settings.NoAutoGain
		u.applyVoiceSettings()
		u.saveVoiceSettings()
	}
	if s.keybindsLink.Clicked(gtx) {
		c.showSettingsPage(pageKeybinds)
	}

	// Keybinds.
	for _, a := range keybindActions {
		if clickOf(s.record, a.id).Clicked(gtx) {
			if s.recording == a.id {
				c.stopRecording()
			} else {
				c.startRecording(a.id)
			}
		}
		if clickOf(s.clear, a.id).Clicked(gtx) {
			if s.recording == a.id {
				c.stopRecording()
			}
			delete(settings.Keybinds, a.id)
			u.saveVoiceSettings()
			u.applyKeybinds()
		}
	}

	// Advanced.
	if s.helloBtn.Clicked(gtx) {
		updateCommunity(func(cs *communitySettings) { cs.NoHello = !cs.NoHello })
	}
	if s.reportsBtn.Clicked(gtx) {
		updateCommunity(func(cs *communitySettings) { cs.NoReports = !cs.NoReports })
	}
	if s.perfBtn.Clicked(gtx) {
		s.perfErr = ""
		if err := u.setPerformanceMode(!u.images.lowPower); err != nil {
			s.perfErr = "Could not save Performance Mode: " + err.Error()
		}
	}

	// The level falls by 60 dB a second, and is redrawn while measured.
	if s.test != nil {
		s.level = max(s.test.Level(), s.level-2)
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(33 * time.Millisecond)})
	}
}

// settingsDeviceItems are the microphones, if input, or the speakers to
// choose from.
func (c *chatView) settingsDeviceItems(input bool) []menuItem {
	u := c.u
	settings := u.voiceSettings()
	devices, cur := u.voice.outputs, settings.Output
	if input {
		devices, cur = u.voice.inputs, settings.Input
	}
	items := make([]menuItem, 0, len(devices)+1)
	for _, d := range append([]voice.Device{{}}, devices...) {
		text := d.Name
		if d.ID == "" {
			text = "Default"
		}
		items = append(items, menuItem{key: d.ID, text: text, check: checked(cur == d.ID), act: func() {
			if input {
				u.setDevices(d.ID, settings.Output)
			} else {
				u.setDevices(settings.Input, d.ID)
			}
			c.restartMicTest()
		}})
	}
	return items
}

// deviceName is the name of the device with the ID id, "Default" for "".
func deviceName(devices []voice.Device, id string) string {
	for _, d := range devices {
		if d.ID == id && id != "" {
			return d.Name
		}
	}
	return "Default"
}

func (c *chatView) layoutSettings(gtx layout.Context) layout.Dimensions {
	c.updateSettings(gtx)
	size := gtx.Constraints.Max
	if !c.settings.open {
		return layout.Dimensions{Size: size}
	}
	// The sidebar takes half of what is left over beyond the page, as in
	// Discord, where both are centered together.
	sideW := min(gtx.Dp(232)+max(size.X-gtx.Dp(232+40+740+72), 0)/2, size.X/2)
	paint.FillShape(gtx.Ops, colorSidebar, clip.Rect{Max: image.Pt(sideW, size.Y)}.Op())
	paint.FillShape(gtx.Ops, colorChat, clip.Rect{Min: image.Pt(sideW, 0), Max: size}.Op())

	listW := max(min(gtx.Dp(218), sideW-gtx.Dp(12)), 0)
	top := gtx.Dp(60)
	t := op.Offset(image.Pt(sideW-listW-gtx.Dp(6), top)).Push(gtx.Ops)
	sgtx := gtx
	sgtx.Constraints = layout.Constraints{Min: image.Pt(listW, 0), Max: image.Pt(listW, max(size.Y-top, 0))}
	c.layoutSettingsSidebar(sgtx)
	t.Pop()

	x := sideW + gtx.Dp(40)
	w := max(min(gtx.Dp(740), size.X-x-gtx.Dp(72)), 0)
	t = op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
	pgtx := gtx
	pgtx.Constraints = layout.Exact(image.Pt(w, size.Y))
	c.layoutSettingsPage(pgtx)
	t.Pop()

	t = op.Offset(image.Pt(x+w+gtx.Dp(16), top)).Push(gtx.Ops)
	c.layoutSettingsClose(gtx)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// layoutSettingsSidebar lists the pages, and the logout under them.
func (c *chatView) layoutSettingsSidebar(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.settings
	row := func(click *widget.Clickable, txt string, col colorT, selected bool, trail *widget.Icon) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
					fg := col
					if col == colorMuted && (hovered || selected) {
						fg = colorHeader
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, oneLine(label(th, 16, fg, txt)).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if trail == nil {
								return layout.Dimensions{}
							}
							return icon(gtx, trail, 16, fg)
						}),
					)
				}, selected)
			})
		})
	}
	heading := func(txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Bottom: unit.Dp(6), Top: unit.Dp(6)}.Layout(gtx, oneLine(bold(label(th, 12, colorMuted, txt))).Layout)
		})
	}
	children := []layout.FlexChild{layout.Rigid(c.layoutSettingsUser), heading("USER SETTINGS")}
	for p := range settingsPages {
		if p == pageVoice {
			children = append(children, heading("APP SETTINGS"))
		}
		children = append(children, row(&s.pages[p], settingsPageNames[p], colorMuted, s.page == p, nil))
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 8) }),
		row(&s.logout, "Log Out", colorRed, false, iconLogout),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutSettingsClose is the round button that closes the settings, with
// ESC under it, as in Discord.
func (c *chatView) layoutSettingsClose(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.settings
	d := gtx.Dp(36)
	s.closeBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		col := colorMuted
		if s.closeBtn.Hovered() {
			col = colorHeader
			paint.FillShape(gtx.Ops, colorHover, clip.Ellipse(image.Rect(0, 0, d, d)).Op(gtx.Ops))
		}
		sw := float32(max(gtx.Dp(2), 1))
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Ellipse(image.Rect(1, 1, d-1, d-1)).Path(gtx.Ops), Width: sw}.Op())
		gtx.Constraints = layout.Exact(image.Pt(d, d))
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconClose, 20, col) })
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
	t := op.Offset(image.Pt(0, d+gtx.Dp(6))).Push(gtx.Ops)
	lgtx := gtx
	lgtx.Constraints = layout.Constraints{Min: image.Pt(d, 0), Max: image.Pt(d, gtx.Dp(20))}
	esc := bold(label(th, 12, colorMuted, "ESC"))
	esc.Alignment = text.Middle
	esc.Layout(lgtx)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(d, d+gtx.Dp(26))}
}

// layoutSettingsPage is the page chosen, scrolled as a whole.
func (c *chatView) layoutSettingsPage(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.settings
	var rows []layout.Widget
	switch s.page {
	case pageVoice:
		rows = c.voiceSettingsRows()
	case pageKeybinds:
		rows = c.keybindRows()
	case pageAccount:
		rows = c.myAccountRows()
	case pageProfiles:
		rows = c.profileRows()
	case pageSocial:
		rows = c.socialRows()
	case pagePrivacy:
		rows = c.privacyRows()
	case pageSounds:
		rows = c.soundRows()
	case pageAppearance:
		rows = c.appearanceRows()
	case pageAccessibility:
		rows = c.accessibilityRows()
	case pageGeneral:
		rows = c.generalRows()
	case pageAdvanced:
		rows = c.advancedRows()
	}
	title := settingsPageNames[s.page]
	rows = append([]layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(60), Bottom: unit.Dp(20)}.Layout(gtx, bold(label(th, 20, colorHeader, title)).Layout)
	}}, rows...)
	rows = append(rows, layout.Spacer{Height: unit.Dp(80)}.Layout)
	return listLayout(gtx, th, &s.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		// Clear of the scroll bar.
		return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, rows[i])
	})
}

// settingsHeading is the small title over a setting.
func settingsHeading(th *material.Theme, txt string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, bold(label(th, 12, colorMuted, txt)).Layout)
	}
}

// settingsText is what a setting does, under its heading.
func settingsText(th *material.Theme, txt string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, label(th, 14, colorMuted, txt).Layout)
	}
}

// settingsDivider is the line between groups of settings, with gap above
// and under it.
func settingsDivider(gtx layout.Context, gap unit.Dp) layout.Dimensions {
	return layout.Inset{Top: gap, Bottom: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(gtx.Constraints.Max.X, max(gtx.Dp(1), 1))
		paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: size}.Op())
		return layout.Dimensions{Size: size}
	})
}

// twoColumns lays out left and right side by side, of the same width.
func twoColumns(left, right layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, left),
			layout.Rigid(layout.Spacer{Width: unit.Dp(20)}.Layout),
			layout.Flexed(1, right),
		)
	}
}

// stacked lays out ws one under the other.
func stacked(ws ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, len(ws))
		for i, w := range ws {
			children[i] = layout.Rigid(w)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func (c *chatView) voiceSettingsRows() []layout.Widget {
	u := c.u
	th := u.th
	s := &c.settings
	settings := u.voiceSettings()
	volume := func(sl *slider, vol float32) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return sl.layout(gtx, 0, vol/2) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if sl.dragging {
						vol = snapVolume(sl.value * 2)
					}
					gtx.Constraints.Min.X = gtx.Dp(52)
					return layout.E.Layout(gtx, label(th, 14, colorText, fmt.Sprintf("%.0f%%", vol*100)).Layout)
				}),
			)
		}
	}
	rows := []layout.Widget{
		twoColumns(
			stacked(settingsHeading(th, "INPUT DEVICE"), func(gtx layout.Context) layout.Dimensions {
				return dropdown(gtx, th, &s.inputBtn, deviceName(u.voice.inputs, settings.Input))
			}),
			stacked(settingsHeading(th, "OUTPUT DEVICE"), func(gtx layout.Context) layout.Dimensions {
				return dropdown(gtx, th, &s.outputBtn, deviceName(u.voice.outputs, settings.Output))
			}),
		),
		layout.Spacer{Height: unit.Dp(20)}.Layout,
		twoColumns(
			stacked(settingsHeading(th, "INPUT VOLUME"), volume(&s.inVol, settings.inputVolume())),
			stacked(settingsHeading(th, "OUTPUT VOLUME"), volume(&s.outVol, settings.outputVolume())),
		),
		layout.Spacer{Height: unit.Dp(20)}.Layout,
		settingsHeading(th, "MIC TEST"),
		settingsText(th, "Say something to see how loud you are. Let's Check also plays your voice back to you."),
		func(gtx layout.Context) layout.Dimensions {
			txt, bg := "Let's Check", colorAccent
			if s.test != nil && s.test.Echoing() {
				txt, bg = "Stop Testing", colorSecondaryButton
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(120)
					return button(gtx, th, &s.checkBtn, bg, txt)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return levelMeter(gtx, s.level, voice.SilentLevel)
				}),
			)
		},
	}
	if s.testErr != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, s.testErr).Layout)
		})
	}
	rows = append(rows,
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsHeading(th, "VOICE PROCESSING"),
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.nsBtn, "Noise Suppression", "Removes the noise around what you say, such as fans, keyboards and traffic.", !settings.NoNoiseSuppression)
		},
		func(gtx layout.Context) layout.Dimensions {
			// How much is removed, while on.
			if settings.NoNoiseSuppression {
				return layout.Dimensions{}
			}
			strength := settings.noiseStrength()
			if s.nsStrength.dragging {
				strength = s.nsStrength.value
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(110)
						return label(th, 14, colorText, "Strength").Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return s.nsStrength.layout(gtx, 0, strength) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(52)
						return layout.E.Layout(gtx, label(th, 14, colorText, fmt.Sprintf("%.0f%%", strength*100)).Layout)
					}),
				)
			})
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.agcBtn, "Automatic Gain Control", "Evens out how loud you are, raising a quiet microphone and lowering a loud one.", !settings.NoAutoGain)
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsHeading(th, "INPUT MODE"),
		func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &s.vadBtn, "Voice Activity", !settings.PushToTalk)
		},
		func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &s.pttBtn, "Push to Talk", settings.PushToTalk)
		},
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	)
	if settings.PushToTalk {
		shortcut := "No shortcut is set yet."
		if keys := settings.Keybinds["push_to_talk"]; len(keys) > 0 {
			shortcut = "Hold " + keys.String() + " to talk."
		}
		rows = append(rows,
			settingsHeading(th, "SHORTCUT"),
			settingsText(th, shortcut),
			func(gtx layout.Context) layout.Dimensions {
				return s.keybindsLink.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return label(th, 14, colorLink, "Change it in Keybinds").Layout(gtx)
				})
			},
		)
		return rows
	}
	sens := settings.sensitivity()
	return append(rows,
		settingsHeading(th, "INPUT SENSITIVITY"),
		settingsText(th, fmt.Sprintf("What you say is sent when it is louder than %.0f dB. Drag the line; the bar turns green where you would be heard.", sens)),
		func(gtx layout.Context) layout.Dimensions {
			return c.layoutSensitivity(gtx, sens)
		},
	)
}

// levelMeter is a bar filled as loud as level is, in dB, yellow below
// threshold and green above it.
func levelMeter(gtx layout.Context, level, threshold float64) layout.Dimensions {
	w, h := gtx.Constraints.Max.X, gtx.Dp(20)
	drawMeter(gtx, image.Rect(0, h/2-gtx.Dp(4), w, h/2+gtx.Dp(4)), level, threshold)
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// drawMeter fills bar as loud as level is, in dB, yellow below threshold and
// green above it.
func drawMeter(gtx layout.Context, bar image.Rectangle, level, threshold float64) {
	r := bar.Dy() / 2
	paint.FillShape(gtx.Ops, colorInput, clip.UniformRRect(bar, r).Op(gtx.Ops))
	at := func(db float64) int {
		return bar.Min.X + int(float64(bar.Dx())*(1-db/voice.SilentLevel))
	}
	lx, tx := at(level), at(threshold)
	if level <= voice.SilentLevel {
		return
	}
	defer clip.UniformRRect(bar, r).Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, colorYellow, clip.Rect{Min: bar.Min, Max: image.Pt(min(lx, tx), bar.Max.Y)}.Op())
	if lx > tx {
		paint.FillShape(gtx.Ops, colorGreen, clip.Rect{Min: image.Pt(tx, bar.Min.Y), Max: image.Pt(lx, bar.Max.Y)}.Op())
	}
}

// layoutSensitivity is the level of the microphone with the line of the
// sensitivity over it, which drags.
func (c *chatView) layoutSensitivity(gtx layout.Context, sens float64) layout.Dimensions {
	s := &c.settings
	w, h := gtx.Constraints.Max.X, gtx.Dp(24)
	thumb := gtx.Dp(6)
	track := max(w-2*thumb, 1)
	if s.sens.dragging {
		sens = float64(voice.SilentLevel) * float64(1-s.sens.value)
	}
	bar := image.Rect(thumb, h/2-gtx.Dp(5), thumb+track, h/2+gtx.Dp(5))
	drawMeter(gtx, bar, s.level, sens)
	x := thumb + int(float64(track)*(1-sens/voice.SilentLevel))
	line := gtx.Dp(2)
	paint.FillShape(gtx.Ops, colorHeader, clip.UniformRRect(image.Rect(x-line, 0, x+line, h), line).Op(gtx.Ops))
	s.sens.input(gtx, thumb, track, h)
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func (c *chatView) keybindRows() []layout.Widget {
	u := c.u
	th := u.th
	rows := []layout.Widget{
		settingsText(th, "Keybinds work in any window, even while "+appName+" is in the background. Click Record, press the keys together, and let go."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	for _, a := range keybindActions {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, stacked(
								bold(label(th, 16, colorHeader, a.name)).Layout,
								label(th, 14, colorMuted, a.about).Layout,
							)),
							layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return c.layoutKeybind(gtx, a.id)
							}),
						)
					})
				})
			})
		})
	}
	return rows
}

// layoutKeybind is the box of the keys of the action id, with the buttons
// to record them and to clear them.
func (c *chatView) layoutKeybind(gtx layout.Context, id string) layout.Dimensions {
	u := c.u
	th := u.th
	s := &c.settings
	keys := u.voiceSettings().Keybinds[id]
	recording := s.recording == id
	txt, col := "No keybind set", colorMuted
	switch {
	case recording:
		txt, col = "Press the keys…", colorHeader
	case len(keys) > 0:
		txt, col = keys.String(), colorHeader
	}
	btn, btnBg := "Record", colorSecondaryButton
	if recording {
		btn, btnBg = "Stop", colorRed
	}
	w := min(gtx.Dp(300), gtx.Constraints.Max.X)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			paint.FillShape(gtx.Ops, colorServers, clip.UniformRRect(r, gtx.Dp(4)).Op(gtx.Ops))
			if recording {
				sw := max(gtx.Dp(1), 1)
				paint.FillShape(gtx.Ops, colorRed, clip.Stroke{Path: clip.UniformRRect(r, gtx.Dp(4)).Path(gtx.Ops), Width: float32(sw)}.Op())
			}
			return layout.Dimensions{Size: r.Max}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(6), Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(bold(label(th, 14, col, txt))).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if len(keys) == 0 || recording {
							return layout.Dimensions{}
						}
						return iconButton(gtx, clickOf(s.clear, id), iconClose, colorMuted, colorRed)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(72)
						return button(gtx, th, clickOf(s.record, id), btnBg, btn)
					}),
				)
			})
		}),
	)
}

func (c *chatView) advancedRows() []layout.Widget {
	u := c.u
	th := u.th
	s := &c.settings
	community := loadCommunity()
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.perfBtn, "Performance Mode", "Shows still pictures in place of animated GIF and APNG previews, which uses less CPU and memory.", u.images.lowPower)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.helloBtn, "Show That I Use Linkcord", "Others who use Linkcord see its logo beside your name. Only your Discord ID and Linkcord's version are sent to Linkcord's server.", !community.NoHello)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.reportsBtn, "Send Error Reports", "When Linkcord crashes or freezes, what it was doing is sent to Linkcord's developers to fix it. No messages, tokens or passwords are sent.", !community.NoReports)
		},
	}
	if s.perfErr != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, colorRed, s.perfErr).Layout)
		})
	}
	return rows
}

// dropdown is a box showing what is chosen, which click opens the choices
// of.
func dropdown(gtx layout.Context, th *material.Theme, click *widget.Clickable, txt string) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		col := colorMuted
		if click.Hovered() {
			col = colorHeader
		}
		return rounded(gtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8), Top: unit.Dp(10), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(label(th, 14, colorText, txt)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconExpand, 20, col) }),
				)
			})
		})
	})
}

// radioRow is a choice among others, with a round box ticked when on.
func radioRow(gtx layout.Context, th *material.Theme, click *widget.Clickable, txt string, on bool) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorSidebar
			switch {
			case on:
				bg = colorSelected
			case click.Hovered():
				bg = colorHover
			}
			return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							d := gtx.Dp(20)
							col := colorMuted
							if on {
								col = colorHeader
							}
							sw := float32(max(gtx.Dp(2), 1))
							paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Ellipse(image.Rect(1, 1, d-1, d-1)).Path(gtx.Ops), Width: sw}.Op())
							if on {
								in := gtx.Dp(5)
								paint.FillShape(gtx.Ops, colorHeader, clip.Ellipse(image.Rect(in, in, d-in, d-in)).Op(gtx.Ops))
							}
							return layout.Dimensions{Size: image.Pt(d, d)}
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						layout.Flexed(1, label(th, 16, colorText, txt).Layout),
					)
				})
			})
		})
	})
}

// switchRow is a setting that is on or off: its name and what it does, and
// a switch at the right, which click turns.
func switchRow(gtx layout.Context, th *material.Theme, click *widget.Clickable, name, about string, on bool) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, stacked(
				label(th, 16, colorHeader, name).Layout,
				label(th, 14, colorMuted, about).Layout,
			)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return switchToggle(gtx, on) }),
		)
	})
}

// switchToggle is a switch, green with its knob at the right when on.
func switchToggle(gtx layout.Context, on bool) layout.Dimensions {
	w, h := gtx.Dp(40), gtx.Dp(24)
	bg := colorSecondaryButton
	if on {
		bg = colorGreen
	}
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rect(0, 0, w, h), h/2).Op(gtx.Ops))
	pad := gtx.Dp(3)
	knob := h - 2*pad
	x := pad
	if on {
		x = w - pad - knob
	}
	paint.FillShape(gtx.Ops, colorHeader, clip.Ellipse(image.Rect(x, pad, x+knob, pad+knob)).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// updateSoundSettings handles each sound's switch, volume and button.
func (c *chatView) updateSoundSettings(gtx layout.Context) {
	u, s := c.u, &c.settings
	settings := u.voiceSettings()
	if s.soundSwitch == nil {
		s.soundSwitch = make(map[string]*widget.Clickable)
		s.soundHear = make(map[string]*widget.Clickable)
		s.soundVolume = make(map[string]*slider)
	}
	for _, sc := range soundChoices {
		if clickOf(s.soundHear, sc.name).Clicked(gtx) {
			u.hearSound(sc.name)
		}
		if clickOf(s.soundSwitch, sc.name).Clicked(gtx) {
			if settings.SoundOff == nil {
				settings.SoundOff = make(map[string]bool)
			}
			if settings.SoundOff[sc.name] {
				delete(settings.SoundOff, sc.name)
			} else {
				settings.SoundOff[sc.name] = true
			}
			u.saveVoiceSettings()
		}
		sl := s.soundVolume[sc.name]
		if sl == nil {
			sl = new(slider)
			s.soundVolume[sc.name] = sl
		}
		if r := sl.update(gtx); r.changed || r.released {
			if settings.SoundVolume == nil {
				settings.SoundVolume = make(map[string]float32)
			}
			settings.SoundVolume[sc.name] = min(max(r.value, 0), 1)
			// Let go, it plays at its new volume.
			if r.released {
				u.saveVoiceSettings()
				u.hearSound(sc.name)
			}
		}
	}
}

// soundRows are the page of Discord's sounds: all of them on or off, and
// each on or off, with its volume and a button to hear it.
func (c *chatView) soundRows() []layout.Widget {
	u := c.u
	th := u.th
	s := &c.settings
	settings := u.voiceSettings()
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &s.soundsBtn, "Play Discord's Sounds", "Of muting and deafening, of people joining and leaving your voice channel and your stream, and of new messages.", !settings.NoSounds)
		},
		func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 24) },
		settingsText(th, "Turn each sound on or off, and choose how loud it plays. The play button lets you hear it."),
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	}
	if s.soundSwitch == nil {
		return rows
	}
	for _, sc := range soundChoices {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			on := !settings.NoSounds && !settings.SoundOff[sc.name]
			vol := settings.soundVolume(sc.name)
			sl := s.soundVolume[sc.name]
			if sl.dragging {
				vol = sl.value
			}
			col := colorText
			if !on {
				col = colorMuted
			}
			return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, clickOf(s.soundHear, sc.name), iconPlay, colorMuted, colorHeader)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Flexed(1, oneLine(label(th, 15, col, sc.label)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return sl.layout(gtx, 180, vol)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(52)
						return layout.E.Layout(gtx, label(th, 14, col, fmt.Sprintf("%.0f%%", vol*100)).Layout)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return clickOf(s.soundSwitch, sc.name).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							return switchToggle(gtx, on)
						})
					}),
				)
			})
		})
	}
	return rows
}
