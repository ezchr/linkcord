//go:build !windows

package gui

// realExecutable is only needed for Chocolatey shims, on Windows.
func realExecutable(path string) string { return path }
