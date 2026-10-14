package gui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// themeColor is a color of the interface that the user can change.
type themeColor struct {
	key, name, about string
	v                *colorT
}

// themeGroup is colors of the interface shown together in the settings.
type themeGroup struct {
	title  string
	colors []themeColor
}

// themeGroups are every color that can be changed, in the order the settings
// list them.
var themeGroups = []themeGroup{
	{"BACKGROUNDS", []themeColor{
		{"servers", "Server List", "The column of servers at the far left.", &colorServers},
		{"sidebar", "Channels and Members", "The channel list, the member list and the settings' side.", &colorSidebar},
		{"panel", "User Panel", "Under the channels: your name, the voice buttons and the call box.", &colorPanel},
		{"chat", "Chat", "Behind the messages, and the settings' pages.", &colorChat},
		{"input", "Message Box and Fields", "Where you write, and the patches behind buttons and bars.", &colorInput},
		{"popup", "Profiles and Popups", "Profile cards and windows over the chat.", &colorPopup},
		{"menu", "Menus", "Right-click menus and dropdowns.", &colorMenu},
		{"code", "Code Blocks", "Behind code in messages.", &colorCode},
	}},
	{"HIGHLIGHTS", []themeColor{
		{"hover", "Hover", "What the pointer is over.", &colorHover},
		{"selected", "Selected", "The channel open, and what is chosen.", &colorSelected},
		{"accent", "Accent", "Main buttons, chosen options and unread pills.", &colorAccent},
		{"mention", "Mention Text", "Mentions of people and roles in messages.", &colorMention},
		{"mentionBg", "Mention Background", "Behind mentions of people and roles.", &colorMentionB},
		{"mentioned", "Messages That Mention You", "The tint and bar of messages that mention you, or @everyone.", &colorMentioned},
	}},
	{"TEXT", []themeColor{
		{"header", "Headings and Names", "The brightest text: titles, names, and text on buttons.", &colorHeader},
		{"text", "Text", "Messages and most of what is written.", &colorText},
		{"muted", "Secondary Text", "Times, hints, descriptions and icons at rest.", &colorMuted},
		{"channel", "Channel Names", "Channel names and their icons.", &colorChannel},
		{"link", "Links", "Links in messages.", &colorLink},
	}},
	{"STATUS AND BUTTONS", []themeColor{
		{"green", "Online and Speaking", "Online status, the speaking ring and switches turned on.", &colorGreen},
		{"greenButton", "Green Buttons", "Such as Add Friend, and the screen being shared.", &colorGreenButton},
		{"secondaryButton", "Gray Buttons", "Secondary buttons, and switches turned off.", &colorSecondaryButton},
		{"red", "Red", "Danger, muted, live and mention counts.", &colorRed},
		{"yellow", "Idle and Warnings", "The idle status and the voice connecting.", &colorYellow},
		{"offline", "Offline", "The offline status.", &colorOffline},
		{"boost", "Boosts", "Server boosts.", &colorBoost},
		{"boostChip", "Boost Goal Label", "The label of the boost goal.", &colorBoostChip},
	}},
}

// themeDefaults are the colors the interface is built with, which presets
// change and reset falls back to.
var themeDefaults = func() map[string]colorT {
	m := make(map[string]colorT)
	for _, g := range themeGroups {
		for _, c := range g.colors {
			m[c.key] = *c.v
		}
	}
	return m
}()

// themePreset is a set of colors chosen together; those it does not name
// stay as in Linkcord Dark.
type themePreset struct {
	key, name string
	colors    map[string]uint32
}

var themePresets = []themePreset{
	{"dark", "Linkcord Dark", nil},
	{"classic", "Discord Classic", map[string]uint32{
		"servers": 0x202225, "sidebar": 0x2f3136, "panel": 0x292b2f, "chat": 0x36393f, "input": 0x40444b,
		"popup": 0x18191c, "menu": 0x18191c, "code": 0x2f3136, "hover": 0x34373c, "selected": 0x42464d,
	}},
	{"amoled", "AMOLED Black", map[string]uint32{
		"servers": 0x000000, "sidebar": 0x050505, "panel": 0x000000, "chat": 0x000000, "input": 0x141414,
		"popup": 0x0a0a0a, "menu": 0x0d0d0d, "code": 0x0d0d0d, "hover": 0x1a1a1a, "selected": 0x262626,
	}},
	{"light", "Light", map[string]uint32{
		"servers": 0xe3e5e8, "sidebar": 0xf2f3f5, "panel": 0xebedef, "chat": 0xffffff, "input": 0xe3e5e8,
		"popup": 0xffffff, "menu": 0xffffff, "code": 0xf2f3f5, "hover": 0xe0e1e5, "selected": 0xd4d7dc,
		"header": 0x060607, "text": 0x313338, "muted": 0x5c5e66, "channel": 0x5c5e66, "link": 0x006ce7,
		"mention": 0x505cdc, "mentionBg": 0xdee0fc, "secondaryButton": 0x6d6f78, "offline": 0x80848e,
	}},
	{"midnight", "Midnight Blue", map[string]uint32{
		"servers": 0x0b0f19, "sidebar": 0x111827, "panel": 0x0d1320, "chat": 0x161e2e, "input": 0x1f2937,
		"popup": 0x0a0e17, "menu": 0x0f1523, "code": 0x0f1523, "hover": 0x1c2536, "selected": 0x273449,
		"accent": 0x3b82f6, "link": 0x60a5fa, "mentionBg": 0x1e3a8a, "mention": 0xbfdbfe,
	}},
	{"dracula", "Dracula", map[string]uint32{
		"servers": 0x191a21, "sidebar": 0x21222c, "panel": 0x1e1f29, "chat": 0x282a36, "input": 0x343746,
		"popup": 0x191a21, "menu": 0x21222c, "code": 0x21222c, "hover": 0x2f3140, "selected": 0x44475a,
		"header": 0xf8f8f2, "text": 0xf8f8f2, "muted": 0xa4a7c0, "channel": 0x8b8fae, "accent": 0xbd93f9,
		"link": 0x8be9fd, "green": 0x50fa7b, "greenButton": 0x2fa85a, "red": 0xff5555, "yellow": 0xf1fa8c,
		"mention": 0xff79c6, "mentionBg": 0x4a3a66, "boost": 0xff79c6,
	}},
	{"nord", "Nord", map[string]uint32{
		"servers": 0x242933, "sidebar": 0x2b303b, "panel": 0x272c36, "chat": 0x2e3440, "input": 0x3b4252,
		"popup": 0x242933, "menu": 0x2b303b, "code": 0x272c36, "hover": 0x353c4a, "selected": 0x434c5e,
		"header": 0xeceff4, "text": 0xd8dee9, "muted": 0xa3acbb, "channel": 0x8d97a8, "accent": 0x5e81ac,
		"link": 0x88c0d0, "green": 0xa3be8c, "greenButton": 0x6f9a5a, "red": 0xbf616a, "yellow": 0xebcb8b,
		"mention": 0x88c0d0, "mentionBg": 0x3b4f6b,
	}},
	{"solarized", "Solarized Dark", map[string]uint32{
		"servers": 0x001f27, "sidebar": 0x00252e, "panel": 0x00222a, "chat": 0x002b36, "input": 0x073642,
		"popup": 0x001f27, "menu": 0x00252e, "code": 0x00252e, "hover": 0x0a3c4a, "selected": 0x11515f,
		"header": 0xeee8d5, "text": 0x93a1a1, "muted": 0x839496, "channel": 0x657b83, "accent": 0x268bd2,
		"link": 0x2aa198, "green": 0x859900, "greenButton": 0x6c7c00, "red": 0xdc322f, "yellow": 0xb58900,
		"mention": 0x2aa198, "mentionBg": 0x0d4a5a,
	}},
	{"forest", "Forest", map[string]uint32{
		"servers": 0x0f1a14, "sidebar": 0x14221a, "panel": 0x111d16, "chat": 0x18291f, "input": 0x213528,
		"popup": 0x0f1a14, "menu": 0x14221a, "code": 0x14221a, "hover": 0x1d3024, "selected": 0x2a4232,
		"accent": 0x2f9e63, "link": 0x5fd39a, "mention": 0xb6f0cf, "mentionBg": 0x23523a,
	}},
	{"rose", "Rose", map[string]uint32{
		"servers": 0x1a1216, "sidebar": 0x22171c, "panel": 0x1d1418, "chat": 0x2a1c22, "input": 0x3a2830,
		"popup": 0x1a1216, "menu": 0x22171c, "code": 0x22171c, "hover": 0x33232a, "selected": 0x46303a,
		"accent": 0xe0457b, "link": 0xff8fb3, "mention": 0xffc2d6, "mentionBg": 0x5a2a3d,
	}},
}

// appearanceSettings are the colors the user chose: a preset, and colors
// changed over it, as #rrggbb by key, kept in appearance.json beside the log.
type appearanceSettings struct {
	Preset string            `json:"preset,omitempty"`
	Colors map[string]string `json:"colors,omitempty"`
}

func appearancePath() string {
	return filepath.Join(filepath.Dir(logPath()), "appearance.json")
}

// loadAppearance reads the colors chosen, none if there are none.
func loadAppearance() appearanceSettings {
	var a appearanceSettings
	if data, err := os.ReadFile(appearancePath()); err == nil {
		if err := json.Unmarshal(data, &a); err != nil {
			slog.Warn("could not read the colors chosen", "err", err)
		}
	}
	return a
}

// saveAppearance writes the colors chosen.
func saveAppearance(a appearanceSettings) {
	data, err := json.Marshal(a)
	if err != nil {
		return
	}
	path := appearancePath()
	go func() {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			err = os.WriteFile(path, data, 0o600)
		}
		if err != nil {
			slog.Warn("could not save the colors chosen", "err", err)
		}
	}()
}

// presetColor is the color key has in the preset named, before the user
// changes it.
func presetColor(preset, key string) colorT {
	for _, p := range themePresets {
		if p.key == preset {
			if v, ok := p.colors[key]; ok {
				return rgb(v)
			}
		}
	}
	return themeDefaults[key]
}

// applyAppearance sets every color of the interface as a says.
func applyAppearance(a appearanceSettings) {
	for _, g := range themeGroups {
		for _, c := range g.colors {
			*c.v = presetColor(a.Preset, c.key)
			if hex, ok := a.Colors[c.key]; ok {
				if v, ok := parseHex(hex); ok {
					*c.v = v
				}
			}
		}
	}
}

// hexOf writes c as #rrggbb.
func hexOf(c colorT) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// parseHex reads #rrggbb, or rrggbb, or #rgb.
func parseHex(s string) (colorT, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return colorT{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return colorT{}, false
	}
	return rgb(uint32(v)), true
}
