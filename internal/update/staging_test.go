package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStagingDir stages updates in the cache, out of the executable's
// folder, keeps the folders of the latest finished updates, and removes those
// left beside the executable.
func TestStagingDir(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "app")
	cache := filepath.Join(root, "cache")
	os.MkdirAll(exeDir, 0o700)
	target := filepath.Join(exeDir, "linkcord.exe")

	finished := func(dir string, age time.Duration) string {
		d, err := os.MkdirTemp(dir, stagePrefix)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "result"), []byte("applied"), 0o600)
		when := time.Now().Add(-age)
		os.Chtimes(d, when, when)
		return d
	}
	os.MkdirAll(cache, 0o700)
	beside := finished(exeDir, time.Hour)
	old := []string{finished(cache, 4*time.Hour), finished(cache, 3*time.Hour), finished(cache, 2*time.Hour)}
	// One whose helper may still run.
	running, _ := os.MkdirTemp(cache, stagePrefix)

	m := &Manager{cacheDir: cache}
	dir, err := m.stagingDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != cache {
		t.Errorf("staged in %s, want in %s", dir, cache)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if exists(beside) {
		t.Error("the folder left beside the executable was kept")
	}
	if exists(old[0]) {
		t.Error("the oldest update's folder was kept")
	}
	if !exists(old[1]) || !exists(old[2]) {
		t.Error("the folders of the latest updates were removed")
	}
	if !exists(running) {
		t.Error("the folder of an update in progress was removed")
	}
}
