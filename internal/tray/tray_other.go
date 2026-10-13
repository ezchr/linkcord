//go:build !windows

// Package tray shows Linkcord's icon in the notification area of the
// taskbar while its window is hidden. Elsewhere than on Windows, there is
// none, and closing the window quits.
package tray

import "errors"

type Tray struct{}

func Start(tip, openText, quitText string, onOpen, onQuit func()) (*Tray, error) {
	return nil, errors.New("tray: only on Windows")
}

func (*Tray) Show(title, text string) error { return nil }
func (*Tray) Hide()                         {}
func (*Tray) Close()                        {}

func HideWindow(uintptr) {}
func ShowWindow(uintptr) {}

func CloseWindow(uintptr) {}
