// Package hotkey acts on combinations of keys pressed anywhere, in Linkcord
// or in any other window, as Discord's keybinds do, and records the
// combination the user presses to choose one.
package hotkey

import (
	"fmt"
	"slices"
	"strings"
)

// Key is a Windows virtual-key code, of a key or of a mouse button.
type Key uint16

// Combo is a combination of keys held together; its modifiers come first.
type Combo []Key

// The keys that Combo puts first, in this order.
const (
	keyShift   Key = 0x10
	keyControl Key = 0x11
	keyAlt     Key = 0x12
	keyLeftWin Key = 0x5b
	keyRgtWin  Key = 0x5c
)

var modifiers = []Key{keyControl, keyShift, keyAlt, keyLeftWin, keyRgtWin}

// sorted returns c with its modifiers first, then its other keys in the
// order they were pressed.
func (c Combo) sorted() Combo {
	var out Combo
	for _, m := range modifiers {
		if slices.Contains(c, m) {
			out = append(out, m)
		}
	}
	for _, k := range c {
		if !slices.Contains(modifiers, k) {
			out = append(out, k)
		}
	}
	return out
}

// String names the keys of c as Discord does, such as "CTRL+SHIFT+M".
func (c Combo) String() string {
	names := make([]string, len(c))
	for i, k := range c {
		names[i] = k.String()
	}
	return strings.Join(names, "+")
}

// String names the key.
func (k Key) String() string {
	switch {
	case k >= '0' && k <= '9', k >= 'A' && k <= 'Z':
		return string(rune(k))
	case k >= 0x60 && k <= 0x69:
		return fmt.Sprintf("NUMPAD %d", k-0x60)
	case k >= 0x70 && k <= 0x87:
		return fmt.Sprintf("F%d", k-0x70+1)
	}
	if name, ok := keyNames[k]; ok {
		return name
	}
	if name := layoutName(k); name != "" {
		return strings.ToUpper(name)
	}
	return fmt.Sprintf("KEY %#02x", uint16(k))
}

var keyNames = map[Key]string{
	0x04: "MOUSE3", 0x05: "MOUSE4", 0x06: "MOUSE5",
	0x08: "BACKSPACE", 0x09: "TAB", 0x0d: "ENTER", 0x13: "PAUSE", 0x14: "CAPS LOCK", 0x1b: "ESC", 0x20: "SPACE",
	keyShift: "SHIFT", keyControl: "CTRL", keyAlt: "ALT", keyLeftWin: "WIN", keyRgtWin: "WIN",
	0x21: "PAGE UP", 0x22: "PAGE DOWN", 0x23: "END", 0x24: "HOME",
	0x25: "LEFT", 0x26: "UP", 0x27: "RIGHT", 0x28: "DOWN",
	0x2c: "PRINT SCREEN", 0x2d: "INSERT", 0x2e: "DELETE", 0x5d: "MENU",
	0x6a: "NUMPAD *", 0x6b: "NUMPAD +", 0x6d: "NUMPAD -", 0x6e: "NUMPAD .", 0x6f: "NUMPAD /",
	0x90: "NUM LOCK", 0x91: "SCROLL LOCK",
	0xad: "VOLUME MUTE", 0xae: "VOLUME DOWN", 0xaf: "VOLUME UP",
	0xb0: "NEXT TRACK", 0xb1: "PREVIOUS TRACK", 0xb2: "STOP", 0xb3: "PLAY/PAUSE",
}

// recordable reports whether k may be part of a recorded combination: not
// the left and right buttons of the mouse, which click to record and stop,
// nor the left and right variants of the modifiers, which also report as
// the modifiers themselves.
func recordable(k Key) bool {
	switch {
	case k == 0x01, k == 0x02:
		return false
	case k >= 0xa0 && k <= 0xa5:
		return false
	}
	return true
}

// Bind is what a combination of keys does.
type Bind struct {
	Keys Combo
	// Down is called when the keys are all held, and Up when one of them is
	// let go after. Either may be nil. They are called on the goroutine that
	// watches the keys, should be quick, and must not call the Watcher.
	Down, Up func()
}
