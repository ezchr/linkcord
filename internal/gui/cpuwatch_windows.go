package gui

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPU is the CPU time Linkcord has used, user and kernel.
func processCPU() (time.Duration, bool) {
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u) != nil {
		return 0, false
	}
	// The times are durations in 100 ns units, not dates.
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100), true
}
