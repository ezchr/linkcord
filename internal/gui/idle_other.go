//go:build !windows

package gui

import "time"

func systemIdle() time.Duration { return 0 }
