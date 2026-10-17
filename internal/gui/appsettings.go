package gui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

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
	// Language is the interface's language: "en" English, "es" Spanish,
	// "" not chosen yet, which takes Windows' language.
	Language string `json:"language,omitempty"`
	// HideMuted are the servers whose muted channels are hidden.
	HideMuted []discord.GuildID `json:"hide_muted,omitempty"`
	// SearchHistory are the last searches, the newest first.
	SearchHistory []string `json:"search_history,omitempty"`
	// PinnedChannels show at the top of their server, and the voice
	// channels of HiddenVoiceNames show who is in them without names.
	PinnedChannels   []discord.ChannelID `json:"pinned_channels,omitempty"`
	HiddenVoiceNames []discord.ChannelID `json:"hidden_voice_names,omitempty"`
	// HideDeleted leaves out the messages others delete, which otherwise
	// stay in their place, marked.
	HideDeleted bool `json:"hide_deleted,omitempty"`
	// NoAutoLevel stops lowering voices that come in saturated, and
	// NoBannerDownload hides the button that saves profiles' banners.
	NoAutoLevel      bool `json:"no_auto_level,omitempty"`
	NoBannerDownload bool `json:"no_banner_download,omitempty"`
	// ClipMinutes is how much of the voice channel is kept for clips, none
	// when zero; an hour at most.
	ClipMinutes int `json:"clip_minutes,omitempty"`
	// StreamClipMinutes is how much of the stream watched is kept for
	// clips, none when zero; ten minutes at most, as streams are big.
	StreamClipMinutes int `json:"stream_clip_minutes,omitempty"`
	// SoundboardMuted are those whose soundboard sounds are not played.
	SoundboardMuted []discord.UserID `json:"soundboard_muted,omitempty"`
	// SoundboardVolumes are how loud each one's soundboard sounds play,
	// from silent to twice as loud, by who, when not as sent.
	SoundboardVolumes map[discord.UserID]float32 `json:"soundboard_volumes,omitempty"`
	// GhostRead reads channels without telling Discord, so that they stay
	// unread everywhere else.
	GhostRead bool `json:"ghost_read,omitempty"`
	// GhostTyping writes without others seeing that the user is typing.
	GhostTyping bool `json:"ghost_typing,omitempty"`
	// SplitLong sends text too long for a message as several messages,
	// rather than as message.txt.
	SplitLong bool `json:"split_long,omitempty"`
	// NoEditHistory stops keeping what messages said before an edit.
	NoEditHistory bool `json:"no_edit_history,omitempty"`
	// NoWhoReacted stops showing who reacted in reactions.
	NoWhoReacted bool `json:"no_who_reacted,omitempty"`
	// NoModInfo hides who is timed out, which Discord shows only to
	// moderators.
	NoModInfo bool `json:"no_mod_info,omitempty"`
	// NoPlatformIcons hides where people are connected from, and
	// NoStaffTags the crowns of a server's owner, admins and managers.
	NoPlatformIcons bool `json:"no_platform_icons,omitempty"`
	NoStaffTags     bool `json:"no_staff_tags,omitempty"`
	// TimeZones are where the user said people live, by user ID, for their
	// profiles to say what time it is for them.
	TimeZones map[string]string `json:"time_zones,omitempty"`
	// VoiceRejoin joins the voice channel again when someone else
	// disconnects the user, VoiceNarrator says aloud who joins and leaves
	// it, and NoVoiceStats stops counting the time in voice.
	VoiceRejoin   bool `json:"voice_rejoin,omitempty"`
	VoiceNarrator bool `json:"voice_narrator,omitempty"`
	NoVoiceStats  bool `json:"no_voice_stats,omitempty"`
	// ShowHiddenChannels lists the channels the user may not see too,
	// dimmed, with what is known of them.
	ShowHiddenChannels bool `json:"show_hidden_channels,omitempty"`
	// Platform is what the others see the user connected from, see
	// platformChoices; empty is the web, as Linkcord is.
	Platform string `json:"platform,omitempty"`
	// NoGameDetection stops showing the games of Discord's list that run
	// as being played.
	NoGameDetection bool `json:"no_game_detection,omitempty"`
	// HiddenActivities are the applications, by ID, never shown on the
	// user's status, with their names.
	HiddenActivities map[string]string `json:"hidden_activities,omitempty"`
	// CustomGames are programs the user added as games, and SeenGames the
	// games detected before, by application ID, with their names.
	CustomGames []customGame        `json:"custom_games,omitempty"`
	SeenGames   map[string]seenGame `json:"seen_games,omitempty"`
	// EditOthers lets the user change what others' messages say, only on
	// their screen.
	EditOthers bool `json:"edit_others,omitempty"`
	// Font is the font the interface is written in, Linkcord's own when
	// empty; see fontChoices.
	Font string `json:"font,omitempty"`
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
	go saveAppSettingsFile(u.app)
}

// saveAppSettingsFile writes the settings to app.json.
func saveAppSettingsFile(a appSettings) {
	data, err := json.Marshal(a)
	if err != nil {
		return
	}
	if err := os.WriteFile(appSettingsPath(), data, 0o600); err != nil {
		slog.Warn("could not save the settings", "err", err)
	}
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

// generalView is the state of the General, Accessibility and Linkcord
// Features pages.
type generalView struct {
	deletedBtn, levelBtn, bannerBtn, clipBtn widget.Clickable
	clipLen                                  slider
	streamClipBtn                            widget.Clickable
	streamClipLen                            slider
	zoom, chatText, groupGap                 slider
	roleColors                               widget.Clickable
	ghostBtn, ghostTypingBtn, editOthersBtn  widget.Clickable
	gamesBtn                                 widget.Clickable
	fakeMuteBtn, fakeDeafenBtn               widget.Clickable
	splitBtn                                 widget.Clickable
	editHistoryBtn                           widget.Clickable
	whoReactedBtn                            widget.Clickable
	modInfoBtn                               widget.Clickable
	platformIconsBtn, staffTagsBtn           widget.Clickable
	rejoinBtn, narratorBtn, statsBtn         widget.Clickable
	statsResetBtn                            widget.Clickable
	hiddenChBtn                              widget.Clickable
	activityBtns                             map[string]*widget.Clickable
	platform                                 platformView
	font                                     fontView
	startup, minimized, tray                 widget.Clickable
	langs                                    [2]widget.Clickable
	startupErr                               string
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
	for i, lang := range []string{"en", "es"} {
		if g.langs[i].Clicked(gtx) && u.app.Language != lang {
			u.app.Language, uiLang = lang, lang
			u.saveAppSettings()
		}
	}
	if g.tray.Clicked(gtx) {
		u.app.QuitOnClose = !u.app.QuitOnClose
		u.saveAppSettings()
	}
	if g.levelBtn.Clicked(gtx) {
		u.app.NoAutoLevel = !u.app.NoAutoLevel
		u.saveAppSettings()
		u.applyLinkcordVoice()
	}
	if g.bannerBtn.Clicked(gtx) {
		u.app.NoBannerDownload = !u.app.NoBannerDownload
		u.saveAppSettings()
	}
	if g.clipBtn.Clicked(gtx) {
		if u.app.ClipMinutes > 0 {
			u.app.ClipMinutes = 0
		} else {
			u.app.ClipMinutes = 10
		}
		u.saveAppSettings()
		u.applyLinkcordVoice()
	}
	if r := g.clipLen.update(gtx); r.changed || r.released {
		u.app.ClipMinutes = max(1, int(1+r.value*59+0.5))
		if r.released {
			u.saveAppSettings()
			u.applyLinkcordVoice()
		}
	}
	if g.streamClipBtn.Clicked(gtx) {
		if u.app.StreamClipMinutes > 0 {
			u.app.StreamClipMinutes = 0
		} else {
			u.app.StreamClipMinutes = 2
		}
		u.saveAppSettings()
		c.applyStreamClip()
	}
	if r := g.streamClipLen.update(gtx); r.changed || r.released {
		u.app.StreamClipMinutes = max(1, int(1+r.value*9+0.5))
		if r.released {
			u.saveAppSettings()
			c.applyStreamClip()
		}
	}
	if g.ghostBtn.Clicked(gtx) {
		u.app.GhostRead = !u.app.GhostRead
		u.saveAppSettings()
	}
	if g.gamesBtn.Clicked(gtx) {
		u.app.NoGameDetection = !u.app.NoGameDetection
		c.gamesOff.Store(u.app.NoGameDetection)
		u.saveAppSettings()
		if u.app.NoGameDetection {
			// Nothing is detected nor registered until it is on again.
			c.games = nil
		}
		if !c.demo {
			c.pushPresence()
		}
	}
	c.updateHiddenActivities(gtx)
	c.updatePlatform(gtx)
	if g.hiddenChBtn.Clicked(gtx) {
		u.app.ShowHiddenChannels = !u.app.ShowHiddenChannels
		u.saveAppSettings()
		c.rebuildRows()
	}
	if g.rejoinBtn.Clicked(gtx) {
		u.app.VoiceRejoin = !u.app.VoiceRejoin
		u.saveAppSettings()
	}
	if g.narratorBtn.Clicked(gtx) {
		u.app.VoiceNarrator = !u.app.VoiceNarrator
		u.saveAppSettings()
		if u.app.VoiceNarrator {
			speak(tr("The narrator is on"))
		} else {
			stopNarrator()
		}
	}
	if g.statsBtn.Clicked(gtx) {
		u.app.NoVoiceStats = !u.app.NoVoiceStats
		u.saveAppSettings()
	}
	if g.platformIconsBtn.Clicked(gtx) {
		u.app.NoPlatformIcons = !u.app.NoPlatformIcons
		u.saveAppSettings()
	}
	if g.staffTagsBtn.Clicked(gtx) {
		u.app.NoStaffTags = !u.app.NoStaffTags
		u.saveAppSettings()
	}
	if g.modInfoBtn.Clicked(gtx) {
		u.app.NoModInfo = !u.app.NoModInfo
		u.saveAppSettings()
	}
	if g.whoReactedBtn.Clicked(gtx) {
		u.app.NoWhoReacted = !u.app.NoWhoReacted
		u.saveAppSettings()
	}
	if g.editHistoryBtn.Clicked(gtx) {
		u.app.NoEditHistory = !u.app.NoEditHistory
		u.saveAppSettings()
		if u.app.NoEditHistory {
			clear(c.edits)
		}
	}
	if g.splitBtn.Clicked(gtx) {
		u.app.SplitLong = !u.app.SplitLong
		u.saveAppSettings()
	}
	if g.fakeMuteBtn.Clicked(gtx) {
		u.voice.fakeMute = !u.voice.fakeMute
		u.applyFake()
	}
	if g.fakeDeafenBtn.Clicked(gtx) {
		u.voice.fakeDeafen = !u.voice.fakeDeafen
		u.applyFake()
	}
	if g.ghostTypingBtn.Clicked(gtx) {
		u.app.GhostTyping = !u.app.GhostTyping
		u.saveAppSettings()
	}
	if g.editOthersBtn.Clicked(gtx) {
		u.app.EditOthers = !u.app.EditOthers
		u.saveAppSettings()
	}
	for _, f := range fontChoices {
		if g.font.btn(f.name).Clicked(gtx) {
			c.setFont(f.name)
		}
	}
	if g.deletedBtn.Clicked(gtx) {
		u.app.HideDeleted = !u.app.HideDeleted
		u.saveAppSettings()
		if u.app.HideDeleted {
			// Those shown go at once.
			c.messages = slices.DeleteFunc(c.messages, func(m discord.Message) bool { return c.deleted.has(m.ID) })
		}
		c.deleted.setOff(u.app.HideDeleted)
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
		layout.Spacer{Height: unit.Dp(24)}.Layout,
		c.fontRows,
	}
}

// fontRows are the choice of the font Linkcord is written in.
func (c *chatView) fontRows(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	f := &c.settings.general.font
	about := "The font Linkcord is written in. Code stays in its own."
	if f.loading != "" {
		about = "Loading " + fontLabel(f.loading) + "…"
	}
	rows := []layout.Widget{
		label(th, 16, colorHeader, "Font").Layout,
		label(th, 14, colorMuted, about).Layout,
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	}
	for _, ch := range installedFonts() {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, f.btn(ch.name), fontLabel(ch.name), ch.name == c.u.app.Font)
		})
	}
	return stacked(rows...)(gtx)
}

// featureRows are the Linkcord Features page: what Linkcord does that
// Discord's app does not.
func (c *chatView) featureRows() []layout.Widget {
	th := c.u.th
	g := &c.settings.general
	return []layout.Widget{
		settingsText(th, "What Linkcord does that Discord's own app does not."),
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.deletedBtn, "Show Deleted Messages",
				"Messages others delete while Linkcord is open stay in their place, marked with a red bin. Only you see them.", !c.u.app.HideDeleted)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.platformIconsBtn, "Platform Icons",
				"Beside names, where each person is connected from: a computer, a phone, the web or a console, in the color of their status there.", !c.u.app.NoPlatformIcons)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.staffTagsBtn, "Staff Crowns",
				"A crown beside the names of a server's admins: gold for its owner and those of its highest admin role, gray for the other admins. Bots' roles do not count.", !c.u.app.NoStaffTags)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.modInfoBtn, "Show Timeouts",
				"Who is timed out, which Discord shows only to moderators, shows to you too: a red clock with how long is left beside their name, and until when in their profile.", !c.u.app.NoModInfo)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.whoReactedBtn, "Who Reacted",
				"Reactions show the pictures of some of those who reacted, beside the count.", !c.u.app.NoWhoReacted)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.editHistoryBtn, "Show Edit History",
				"What messages said before they were edited is kept while Linkcord is open: their \"(edited)\" shows it. Only you see it.", !c.u.app.NoEditHistory)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.ghostBtn, "Read Without Marking as Read",
				"Opening a channel does not tell Discord you saw it: it stays unread in Discord's app, and in Linkcord too. Right-click Mark As Read still marks it. Sending a message there marks it, as Discord does.", c.u.app.GhostRead)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		c.platformRows,
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.rejoinBtn, "Join Voice Again",
				"When someone disconnects you from a voice channel, Linkcord joins it again a second later.", c.u.app.VoiceRejoin)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.narratorBtn, "Voice Narrator",
				"A voice says aloud who joins and leaves the voice channel you are in, with Windows' own voice.", c.u.app.VoiceNarrator)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.statsBtn, "Voice Statistics",
				"How long you spend in voice, in which channels and with whom. Right-click someone, then Follow in Voice, to join the channels they go to.", !c.u.app.NoVoiceStats)
		},
		c.voiceStatsRows,
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.fakeMuteBtn, "Fake Mute",
				"In voice channels the others see you muted, while what you say still reaches them. Off again when Linkcord closes.", c.u.voice.fakeMute)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.fakeDeafenBtn, "Fake Deafen",
				"In voice channels the others see you deafened, while you still hear them, and they you unless muted. Off again when Linkcord closes.", c.u.voice.fakeDeafen)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.hiddenChBtn, "Show Hidden Channels",
				"Channels of servers you may not see are listed too, dimmed with a lock: opening one shows its topic, when it was last written in and who can see it, not its messages.", c.u.app.ShowHiddenChannels)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.splitBtn, "Split Long Messages",
				"Text longer than a message may be is sent as several messages, cut at lines or spaces, rather than as message.txt. Code blocks cut in two stay code.", c.u.app.SplitLong)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.ghostTypingBtn, "Type Without Showing Typing",
				"Others do not see \"is typing…\" while you write; your messages still arrive as always.", c.u.app.GhostTyping)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.editOthersBtn, "Edit Others' Messages",
				"Others' messages get the edit button too, to change what they say on your screen only. Nobody else sees it, and it goes when Linkcord closes; emptying the text brings the original back.", c.u.app.EditOthers)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.levelBtn, "Lower Saturated Voices",
				"When someone's microphone comes in saturated, too loud, they are lowered for you until it is not, and come back up slowly.", !c.u.app.NoAutoLevel)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.bannerBtn, "Download Banners",
				"Profiles' banners have a button that saves them to your downloads.", !c.u.app.NoBannerDownload)
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.clipBtn, "Voice Clips",
				"Keeps what is said in the voice channel you are in, for a clip of the last minutes: the clip button by the voice buttons makes it, of everyone or whom you choose, together and by person, to hear and save.", c.u.app.ClipMinutes > 0)
		},
		func(gtx layout.Context) layout.Dimensions {
			if c.u.app.ClipMinutes == 0 {
				return layout.Dimensions{}
			}
			mins := c.u.app.ClipMinutes
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, sliderRow(th, "Longest Clip",
				"How much is kept, an hour at most. An hour of one person talking takes about 30 MB of memory.",
				&g.clipLen, float32(mins-1)/59, fmt.Sprintf("%d min", mins)))
		},
		layout.Spacer{Height: unit.Dp(16)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.streamClipBtn, "Stream Clips",
				"Keeps the stream you watch, as it comes, for a clip of its last minutes, saved as a video with its sound by the clip button beside Stop Watching.", c.u.app.StreamClipMinutes > 0)
		},
		func(gtx layout.Context) layout.Dimensions {
			mins := c.u.app.StreamClipMinutes
			if mins == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, sliderRow(th, "Longest Stream Clip",
				"How much of the stream is kept, ten minutes at most: a minute of a stream takes about 20 to 50 MB of memory.",
				&g.streamClipLen, float32(mins-1)/9, fmt.Sprintf("%d min", mins)))
		},
	}
}

// generalRows are the General page.
func (c *chatView) generalRows() []layout.Widget {
	u := c.u
	th := u.th
	g := &c.settings.general
	rows := []layout.Widget{
		// The language is named in itself, as Discord lists them.
		bold(label(th, 12, colorMuted, "LANGUAGE · IDIOMA")).Layout,
		layout.Spacer{Height: unit.Dp(6)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &g.langs[0], "English", u.app.Language != "es")
		},
		func(gtx layout.Context) layout.Dimensions {
			return radioRow(gtx, th, &g.langs[1], "Español", u.app.Language == "es")
		},
		layout.Spacer{Height: unit.Dp(20)}.Layout,
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
