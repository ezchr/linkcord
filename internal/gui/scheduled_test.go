package gui

import (
	"testing"
	"time"
)

func TestParseClock(t *testing.T) {
	now := time.Date(2026, 10, 15, 20, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"21:30":  time.Date(2026, 10, 15, 21, 30, 0, 0, time.Local),
		" 8:05 ": time.Date(2026, 10, 16, 8, 5, 0, 0, time.Local),
		"20:00":  time.Date(2026, 10, 16, 20, 0, 0, 0, time.Local),
		"23:59":  time.Date(2026, 10, 15, 23, 59, 0, 0, time.Local),
	} {
		got, ok := parseClock(in, now)
		if !ok || !got.Equal(want) {
			t.Errorf("%q: got %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "18", "25:00", "10:61", "a:b", "-1:30"} {
		if _, ok := parseClock(bad, now); ok {
			t.Errorf("%q taken", bad)
		}
	}
}
