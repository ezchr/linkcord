package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type performanceSettings struct {
	Enabled bool `json:"enabled"`
}

func performanceSettingsPath() string {
	return filepath.Join(filepath.Dir(logPath()), "performance.json")
}

func performanceModeEnabled() bool {
	if value, ok := os.LookupEnv("LINKCORD_LOW_POWER"); ok {
		return value == "1"
	}
	data, err := os.ReadFile(performanceSettingsPath())
	if err != nil {
		return false
	}
	var settings performanceSettings
	return json.Unmarshal(data, &settings) == nil && settings.Enabled
}

func savePerformanceMode(enabled bool) error {
	path := performanceSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings folder: %w", err)
	}
	data, err := json.Marshal(performanceSettings{Enabled: enabled})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Each cache keeps an immutable playback policy. Workers finishing on the old
// cache cannot publish stale frames into the replacement cache after a toggle.
func (u *ui) setPerformanceMode(enabled bool) error {
	if err := savePerformanceMode(enabled); err != nil {
		return err
	}
	next := newImages(u)
	next.lowPower = enabled
	u.images = next
	u.win.Invalidate()
	return nil
}
