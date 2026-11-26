package http

import (
	"testing"
	"time"
)

func TestWindowsToIANA(t *testing.T) {
	for key, want := range map[string]string{
		"Pacific Standard Time":   "America/Los_Angeles",
		"W. Europe Standard Time": "Europe/Berlin",
		"Romance Standard Time":   "Europe/Paris",
		"India Standard Time":     "Asia/Calcutta",
		"UTC":                     "UTC",
		"Tokyo Standard Time ":    "Asia/Tokyo",
		"Made Up Standard Time":   "",
	} {
		if got := windowsToIANA(key); got != want {
			t.Errorf("windowsToIANA(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestWindowsZonesLoad(t *testing.T) {
	// Every mapped zone is one Go knows, so none is a typo.
	for key, name := range windowsZones {
		if _, err := time.LoadLocation(name); err != nil {
			t.Errorf("%s: %s: %v", key, name, err)
		}
	}
}

func TestValidIANA(t *testing.T) {
	for name, want := range map[string]bool{
		"Local": false, "": false, ":/etc/localtime": false,
		"UTC": true, "Europe/Madrid": true,
	} {
		if got := validIANA(name); got != want {
			t.Errorf("validIANA(%q) = %v", name, got)
		}
	}
}
