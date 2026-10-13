package gui

import (
	"os"
	"testing"
)

func TestPerformanceModePreference(t *testing.T) {
	// Keep all settings away from the real application profile.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LINKCORD_LOW_POWER", "")
	if err := os.Unsetenv("LINKCORD_LOW_POWER"); err != nil {
		t.Fatal(err)
	}
	if performanceModeEnabled() {
		t.Fatal("performance mode must default to off")
	}
	for _, enabled := range []bool{true, false, true} {
		if err := savePerformanceMode(enabled); err != nil {
			t.Fatal(err)
		}
		if got := performanceModeEnabled(); got != enabled {
			t.Fatalf("saved preference = %v, want %v", got, enabled)
		}
	}
	t.Setenv("LINKCORD_LOW_POWER", "0")
	if performanceModeEnabled() {
		t.Fatal("explicit environment override was ignored")
	}
	t.Setenv("LINKCORD_LOW_POWER", "1")
	if !performanceModeEnabled() {
		t.Fatal("low-power launch override was ignored")
	}
}
