//go:build !windows

package gui

// runningExecutables finds no games but on Windows.
func runningExecutables() []string { return nil }
