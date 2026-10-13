//go:build !windows

package gui

func knownDownloadsDir() string { return "" }

func showInFolder(string) {}
