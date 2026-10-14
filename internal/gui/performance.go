package gui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
)

// LINKCORD_PROFILE enables local diagnostics for a controlled session.
// Ordinary launches do not create profiles or run diagnostic garbage collection.
func (u *ui) startPerformanceProfile() func() {
	dir := os.Getenv("LINKCORD_PROFILE")
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("could not create profile folder", "err", err)
		return func() {}
	}
	cpu, err := os.Create(filepath.Join(dir, "cpu.pprof"))
	if err != nil {
		slog.Warn("could not create CPU profile", "err", err)
		return func() {}
	}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		cpu.Close()
		slog.Warn("could not start CPU profile", "err", err)
		return func() {}
	}
	started := time.Now()
	var once sync.Once
	stop := func() {
		once.Do(func() { u.writeProfile(dir, cpu, started) })
	}
	// LINKCORD_PROFILE_SECONDS ends the profile after that long, with
	// Linkcord still open, as an idle window is what is measured.
	if n, err := strconv.Atoi(os.Getenv("LINKCORD_PROFILE_SECONDS")); err == nil && n > 0 {
		time.AfterFunc(time.Duration(n)*time.Second, stop)
	}
	return stop
}

func (u *ui) writeProfile(dir string, cpu *os.File, started time.Time) {
	{
		pprof.StopCPUProfile()
		cpu.Close()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		runtime.GC()
		runtime.ReadMemStats(&after)
		summary, _ := json.MarshalIndent(map[string]any{
			"elapsed_seconds": time.Since(started).Seconds(),
			"rendered_frames": u.frameCount,
			"heap_before_gc":  before.HeapAlloc,
			"heap_after_gc":   after.HeapAlloc,
			"heap_reserved":   after.HeapSys,
			"total_allocated": after.TotalAlloc,
			"gc_cycles":       after.NumGC,
		}, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "summary.json"), summary, 0o600)
		heap, err := os.Create(filepath.Join(dir, "heap.pprof"))
		if err != nil {
			slog.Warn("could not create heap profile", "err", err)
			return
		}
		defer heap.Close()
		if err := pprof.WriteHeapProfile(heap); err != nil {
			slog.Warn("could not write heap profile", "err", err)
		}
		runtime.KeepAlive(u)
	}
}

// slowFrame writes frames slower than 30 ms to frames.log in the profile
// folder: how long the posted work, the layout and the drawing took.
func (u *ui) slowFrame(start time.Time, pending, laid time.Duration) {
	dir := os.Getenv("LINKCORD_PROFILE")
	total := time.Since(start)
	if dir == "" || total < 30*time.Millisecond {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "frames.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s total=%v pending=%v layout=%v draw=%v\n", start.Format("15:04:05.000"),
		total.Round(time.Millisecond), pending.Round(time.Millisecond), (laid - pending).Round(time.Millisecond), (total - laid).Round(time.Millisecond))
}
