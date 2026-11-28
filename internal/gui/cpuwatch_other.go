//go:build !windows

package gui

import "time"

func processCPU() (time.Duration, bool) { return 0, false }
