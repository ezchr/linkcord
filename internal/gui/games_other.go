//go:build !windows

package gui

// runningExecutables finds no games but on Windows.
func runningExecutables() []string { return nil }

// windowExecutables finds none but on Windows.
func windowExecutables() []string { return nil }
