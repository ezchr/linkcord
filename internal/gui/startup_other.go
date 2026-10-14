//go:build !windows

package gui

import "errors"

func startsWithWindows() bool { return false }

func setStartWithWindows(bool, bool) error { return errors.New("only on Windows") }
