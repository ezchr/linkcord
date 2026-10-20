//go:build !windows

package gui

// waitAddrChange is only watched on Windows.
func waitAddrChange() bool { return false }
