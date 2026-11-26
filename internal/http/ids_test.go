package http

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Keep the tests' ids out of the real config dir.
	dir, err := os.MkdirTemp("", "lchttp")
	if err != nil {
		panic(err)
	}
	idsPath = func() string { return filepath.Join(dir, "client_ids.json") }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// resetIDs forgets the loaded ids and uses a fresh file and clock.
func resetIDs(t *testing.T, clock *time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client_ids.json")
	oldPath, oldNow := idsPath, now
	idsPath = func() string { return path }
	now = func() time.Time { return *clock }
	idsMu.Lock()
	idsLoaded, ids, idsSaved = false, storedIDs{}, time.Time{}
	idsMu.Unlock()
	t.Cleanup(func() {
		idsPath, now = oldPath, oldNow
		idsMu.Lock()
		idsLoaded, ids = false, storedIDs{}
		idsMu.Unlock()
	})
}

func reload() {
	idsMu.Lock()
	idsLoaded, ids = false, storedIDs{}
	idsMu.Unlock()
}

func TestHeartbeatSessionExpiry(t *testing.T) {
	clock := time.UnixMilli(1_700_000_000_000)
	resetIDs(t, &clock)

	first := HeartbeatSessionID()
	if first == "" {
		t.Fatal("no session made")
	}
	clock = clock.Add(20 * time.Minute)
	if TouchHeartbeatSession() {
		t.Fatal("touch within 30 minutes replaced the session")
	}
	clock = clock.Add(29 * time.Minute)
	if got := HeartbeatSessionID(); got != first {
		t.Fatalf("session replaced 29 minutes after use: %s != %s", got, first)
	}
	// Reading is not use: 30 minutes after the last touch it expires.
	clock = clock.Add(time.Minute)
	if !TouchHeartbeatSession() {
		t.Fatal("touch after 30 minutes unused kept the session")
	}
	second, created := HeartbeatSession()
	if second == first || created != clock.UnixMilli() {
		t.Fatalf("new session %s created %d", second, created)
	}

	// It survives a restart.
	reload()
	if got := HeartbeatSessionID(); got != second {
		t.Fatalf("session not kept between starts: %s != %s", got, second)
	}

	ResetHeartbeatSession()
	if got := HeartbeatSessionID(); got == second {
		t.Fatal("reset kept the session")
	}
}

func TestInstallationIDSetOnce(t *testing.T) {
	clock := time.Now()
	resetIDs(t, &clock)
	if InstallationID() != "" {
		t.Fatal("installation id before any was given")
	}
	SetInstallationID("a.b")
	SetInstallationID("c.d")
	ResetHeartbeatSession()
	reload()
	if got := InstallationID(); got != "a.b" {
		t.Fatalf("installation id %q, want the first one given", got)
	}
	if got := IdentifyProperties()["installation_id"]; got != "a.b" {
		t.Fatalf("identify installation_id %v", got)
	}
	if got := Headers().Get("X-Installation-ID"); got != "a.b" {
		t.Fatalf("X-Installation-ID %q", got)
	}
}
