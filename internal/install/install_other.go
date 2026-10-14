//go:build !windows

// Package install keeps one copy of Linkcord with a shortcut to it on the
// desktop. Elsewhere than on Windows, it does nothing.
package install

const PortableEnvVar = "LINKCORD_PORTABLE"

func Ensure(version string, newer func(a, b string) (bool, error)) bool { return false }
