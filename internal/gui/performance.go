package gui

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
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
	return func() {
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
