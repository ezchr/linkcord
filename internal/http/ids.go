package http

import (
	json "encoding/json/v2"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	"github.com/ezchr/linkcord/internal/consts"
)

// heartbeatSessionTTL is how long client_heartbeat_session_id lives unused
// before a new one is made, as the web client's module 604594 (30 minutes).
const heartbeatSessionTTL = 30 * time.Minute

// heartbeatSaveEvery is how often a touched session is written to disk at
// most, as the web client also writes LAST_CLIENT_HEARTBEAT_SESSION only now
// and then rather than on every touch.
const heartbeatSaveEvery = time.Minute

// heartbeatSession is the web client's LAST_CLIENT_HEARTBEAT_SESSION.
type heartbeatSession struct {
	UUID               string `json:"uuid"`
	CreatedAtTimestamp int64  `json:"createdAtTimestamp"`
	LastUsedTimestamp  int64  `json:"lastUsedTimestamp"`
	Version            int    `json:"version"`
}

func (s *heartbeatSession) expired(now time.Time) bool {
	return s == nil || s.Version != 1 || s.UUID == "" ||
		now.UnixMilli()-s.LastUsedTimestamp >= heartbeatSessionTTL.Milliseconds()
}

// storedIDs are the ids kept between starts: the installation id, which the
// web client keeps in localStorage installation_id_v3 and never clears, and
// the last heartbeat session.
type storedIDs struct {
	InstallationID   string            `json:"installation_id,omitempty"`
	HeartbeatSession *heartbeatSession `json:"heartbeat_session,omitempty"`
}

var (
	idsMu     sync.Mutex
	idsLoaded bool
	ids       storedIDs
	idsSaved  time.Time
	// now is the clock, which tests replace.
	now = time.Now
	// idsPath is where the ids are kept, next to the config file; tests
	// replace it.
	idsPath = func() string {
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = "."
		}
		return filepath.Join(dir, consts.Name, "client_ids.json")
	}
)

// loadIDs reads the stored ids once. idsMu must be held.
func loadIDs() {
	if idsLoaded {
		return
	}
	idsLoaded = true
	b, err := os.ReadFile(idsPath())
	if err != nil {
		return
	}
	if err := json.Unmarshal(b, &ids); err != nil {
		slog.Warn("failed to read stored client ids", "err", err)
		ids = storedIDs{}
	}
}

// saveIDs writes the stored ids. idsMu must be held.
func saveIDs() {
	idsSaved = now()
	b, err := json.Marshal(ids)
	if err != nil {
		return
	}
	path := idsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("failed to save client ids", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		slog.Warn("failed to save client ids", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("failed to save client ids", "err", err)
	}
}

// InstallationID is the installation id Discord gave this client, sent as
// X-Installation-ID and in IDENTIFY, or "" while none is known.
func InstallationID() string {
	idsMu.Lock()
	defer idsMu.Unlock()
	loadIDs()
	return ids.InstallationID
}

// SetInstallationID keeps the installation id from /experiments. As on the
// web it is set once: a stored id is never replaced, and logging out keeps it.
func SetInstallationID(id string) {
	if id == "" {
		return
	}
	idsMu.Lock()
	defer idsMu.Unlock()
	loadIDs()
	if ids.InstallationID != "" {
		return
	}
	ids.InstallationID = id
	saveIDs()
}

// currentSession returns the live heartbeat session, making one when there
// is none or the last expired. idsMu must be held.
func currentSession(t time.Time, touch bool) (s *heartbeatSession, changed bool) {
	loadIDs()
	s = ids.HeartbeatSession
	if s.expired(t) {
		ms := t.UnixMilli()
		s = &heartbeatSession{UUID: uuid.NewV4().String(), CreatedAtTimestamp: ms, LastUsedTimestamp: ms, Version: 1}
		ids.HeartbeatSession = s
		saveIDs()
		return s, true
	}
	if touch {
		s.LastUsedTimestamp = t.UnixMilli()
		if t.Sub(idsSaved) >= heartbeatSaveEvery {
			saveIDs()
		}
	}
	return s, false
}

// HeartbeatSessionID is the current client_heartbeat_session_id, as the
// super properties and analytics events carry it at the time they are made.
// Reading it does not count as use; when none is live a new one is made.
func HeartbeatSessionID() string {
	idsMu.Lock()
	defer idsMu.Unlock()
	s, _ := currentSession(now(), false)
	return s.UUID
}

// HeartbeatSession is the current client_heartbeat_session_id and when it
// was made, in Unix milliseconds, as UPDATE_TIME_SPENT_SESSION_ID sends them.
func HeartbeatSession() (id string, createdAt int64) {
	idsMu.Lock()
	defer idsMu.Unlock()
	s, _ := currentSession(now(), false)
	return s.UUID, s.CreatedAtTimestamp
}

// TouchHeartbeatSession marks the heartbeat session used now, as the web
// client does on each change of activity, focus or app state. A session
// unused for 30 minutes is replaced; changed says this call replaced it,
// when the web client sends UPDATE_TIME_SPENT_SESSION_ID.
func TouchHeartbeatSession() (changed bool) {
	idsMu.Lock()
	defer idsMu.Unlock()
	_, changed = currentSession(now(), true)
	return changed
}

// ResetHeartbeatSession drops the heartbeat session, as the web client does
// when the auth token changes (login, logout, switching accounts); the next
// use makes a new one.
func ResetHeartbeatSession() {
	idsMu.Lock()
	defer idsMu.Unlock()
	loadIDs()
	ids.HeartbeatSession = nil
	saveIDs()
}

// LaunchID is this run's client_launch_id.
func LaunchID() string { return launchID }
