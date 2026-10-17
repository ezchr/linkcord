package gui

import (
	"runtime"
	"testing"
	"time"
)

// TestWindowExecutables lists the programs with a window, quickly, and not
// Linkcord's tests themselves.
func TestWindowExecutables(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip()
	}
	start := time.Now()
	exes := windowExecutables()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	t.Logf("%d programs with a window, in %v", len(exes), time.Since(start))
}
