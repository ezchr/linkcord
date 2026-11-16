package gui

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestColumnOrder(t *testing.T) {
	S, C, M, X := paneServers, paneChannels, paneChat, paneSide
	cases := []struct {
		l    paneLayout
		side bool
		want []paneKind
	}{
		{paneLayout{}, true, []paneKind{S, C, M, X}},
		{paneLayout{}, false, []paneKind{S, C, M}},
		{paneLayout{Servers: placeRight}, true, []paneKind{C, M, X, S}},
		{paneLayout{Channels: placeRight}, true, []paneKind{S, M, X, C}},
		{paneLayout{Servers: placeRight, Channels: placeRight}, true, []paneKind{M, X, C, S}},
		{paneLayout{Members: placeLeft}, true, []paneKind{S, C, X, M}},
		{paneLayout{Servers: placeTop}, true, []paneKind{C, M, X}},
		{paneLayout{Servers: placeBottom, Channels: placeRight, Members: placeLeft}, true, []paneKind{X, M, C}},
		// Places not known are the default.
		{paneLayout{Servers: "middle", Channels: placeTop}, true, []paneKind{S, C, M, X}},
	}
	for _, tc := range cases {
		if got := columnOrder(tc.l, tc.side); !slices.Equal(got, tc.want) {
			t.Errorf("%+v side %v: got %v, want %v", tc.l, tc.side, got, tc.want)
		}
	}
}

func TestPlacePanes(t *testing.T) {
	area := image.Rect(0, 10, 1000, 610)
	r := placePanes(paneLayout{}, area, 72, 240, 200, true)
	if want := image.Rect(0, 10, 72, 610); r.servers != want {
		t.Errorf("servers %v, want %v", r.servers, want)
	}
	if want := image.Rect(72, 10, 312, 610); r.channels != want {
		t.Errorf("channels %v, want %v", r.channels, want)
	}
	if want := image.Rect(312, 10, 800, 610); r.chat != want {
		t.Errorf("chat %v, want %v", r.chat, want)
	}
	if want := image.Rect(800, 10, 1000, 610); r.side != want {
		t.Errorf("members %v, want %v", r.side, want)
	}

	// The server strip at the bottom, the channels at the right.
	r = placePanes(paneLayout{Servers: placeBottom, Channels: placeRight}, area, 72, 240, 200, false)
	if want := image.Rect(0, 538, 1000, 610); r.servers != want {
		t.Errorf("strip %v, want %v", r.servers, want)
	}
	if want := image.Rect(0, 10, 760, 538); r.chat != want {
		t.Errorf("chat %v, want %v", r.chat, want)
	}
	if want := image.Rect(760, 10, 1000, 538); r.channels != want {
		t.Errorf("channels %v, want %v", r.channels, want)
	}
	if !r.side.Empty() {
		t.Errorf("members shown at %v", r.side)
	}

	// The strip at the top.
	r = placePanes(paneLayout{Servers: placeTop}, area, 72, 240, 200, true)
	if want := image.Rect(0, 10, 1000, 82); r.servers != want {
		t.Errorf("strip %v, want %v", r.servers, want)
	}
	if r.chat.Min.Y != 82 || r.chat.Max.Y != 610 {
		t.Errorf("chat %v under the strip", r.chat)
	}

	// Too narrow, the chat gets nothing rather than less than nothing.
	r = placePanes(paneLayout{}, image.Rect(0, 0, 300, 100), 72, 240, 200, true)
	if r.chat.Dx() != 0 || r.side.Max.X > 300 {
		t.Errorf("narrow: chat %v side %v", r.chat, r.side)
	}
}

func TestPaneLayoutSaved(t *testing.T) {
	l := paneLayout{Servers: placeLeft, Channels: placeRight, Members: placeRight, UserPanel: placeTop}.saved()
	if want := (paneLayout{Channels: placeRight, UserPanel: placeTop}); l != want {
		t.Errorf("saved %+v, want %+v", l, want)
	}
	if (paneLayout{Servers: placeBottom}).serversAcross() != true || (paneLayout{}).serversAcross() {
		t.Error("serversAcross wrong")
	}
}

// TestAppearanceFiles checks that appearance.json written before pictures
// and layouts still loads, and that what is saved now loads back the same.
func TestAppearanceFiles(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(appearancePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"preset":"nord","colors":{"accent":"#ff0000"}}`
	if err := os.WriteFile(appearancePath(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	a := loadAppearance()
	if a.Preset != "nord" || a.Colors["accent"] != "#ff0000" || a.Picture != nil || a.Layout != (paneLayout{}) {
		t.Fatalf("old file read as %+v", a)
	}
	// The old file writes back as it was: nothing new in it.
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != old {
		t.Errorf("old settings written as %s", data)
	}

	a.Picture = &pictureSettings{Path: `C:\pics\sky.jpg`, Dim: 40, Blur: 3, Colors: map[string]string{"chat": "#101820"}}
	a.Preset = pictureKey
	a.Layout = paneLayout{Servers: placeBottom, Members: placeLeft}
	data, err = json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back appearanceSettings
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Preset != pictureKey || back.Layout != a.Layout || back.Picture == nil || back.Picture.Path != a.Picture.Path {
		t.Errorf("read back as %+v", back)
	}
	if back.Picture.Dim != 40 || back.Picture.Blur != 3 || back.Picture.Colors["chat"] != "#101820" {
		t.Errorf("picture read back as %+v", back.Picture)
	}

	// The picture's colors are the preset's, under the user's own.
	defer applyAppearance(appearanceSettings{})
	back.Colors = map[string]string{"text": "#eeeeee"}
	applyAppearance(back)
	if colorChat != rgb(0x101820) || colorText != rgb(0xeeeeee) {
		t.Errorf("chat %v text %v", colorChat, colorText)
	}
	if presetColor(pictureKey, "chat") != rgb(0x101820) || presetColor(pictureKey, "green") != themeDefaults["green"] {
		t.Error("presetColor of the picture wrong")
	}
}
