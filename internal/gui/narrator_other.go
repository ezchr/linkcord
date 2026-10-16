//go:build !windows

package gui

// speak says nothing but on Windows.
func speak(string) {}

func stopNarrator() {}
