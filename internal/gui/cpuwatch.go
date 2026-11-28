package gui

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

// watchCPU records, once per run, a CPU profile beside the log when Linkcord
// keeps using much of the CPU, so what did can be found afterwards: high use
// is seldom there when looked for. It is skipped while a profile is taken on
// purpose.
func watchCPU() {
	if os.Getenv("LINKCORD_PROFILE") != "" {
		return
	}
	const (
		every   = 10 * time.Second
		limit   = 0.20 // of all the CPUs
		needed  = 3    // checks in a row above it
		profile = 20 * time.Second
	)
	cpus := float64(runtime.NumCPU())
	last, ok := processCPU()
	if !ok {
		return
	}
	lastAt, high := time.Now(), 0
	for range time.Tick(every) {
		now, ok := processCPU()
		if !ok {
			return
		}
		use := (now - last).Seconds() / time.Since(lastAt).Seconds() / cpus
		last, lastAt = now, time.Now()
		if use < limit {
			high = 0
			continue
		}
		if high++; high < needed {
			continue
		}
		path := filepath.Join(filepath.Dir(logPath()), "cpu-high.pprof")
		f, err := os.Create(path)
		if err != nil {
			return
		}
		slog.Warn("high CPU use; recording a profile", "use", use, "path", path)
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return
		}
		time.Sleep(profile)
		pprof.StopCPUProfile()
		f.Close()
		return
	}
}
