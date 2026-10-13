//go:build !windows

// Package instance keeps Linkcord to one window per Windows session.
package instance

// WindowClass is the window class of Linkcord on Windows.
const WindowClass = "Linkcord"

// Acquire always succeeds outside Windows, where Linkcord is not released.
func Acquire() bool { return true }
