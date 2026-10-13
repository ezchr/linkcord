package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func isolateBuild(t *testing.T) {
	t.Helper()
	old := currentBuild.Load()
	currentBuild.Store(0)
	t.Cleanup(func() { currentBuild.Store(old) })
}

func TestParseBuild(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{`window.GLOBAL_ENV={"BUILD_NUMBER":"634304"}`, 634304},
		{`{"buildNumber":634305}`, 634305},
		{`buildNumber=634306`, 634306},
		{`{"BUILD_NUMBER":0}`, 0},
		{`{"BUILD_NUMBER":12345678901}`, 0},
		{`nothing here`, 0},
	} {
		got, ok := parseBuild([]byte(tc.text))
		if got != tc.want || ok != (tc.want != 0) {
			t.Errorf("%q: got %d/%v, want %d", tc.text, got, ok, tc.want)
		}
	}
}

func TestClientBuildCache(t *testing.T) {
	isolateBuild(t)
	path := filepath.Join(t.TempDir(), "discord-build.json")
	if got := InitializeClientBuild(path); got.Number != ClientBuildNumber || got.Source != "builtin" {
		t.Fatal(got)
	}
	cached := BuildInfo{Number: ClientBuildNumber + 100, FetchedAt: time.Now().UTC()}
	if err := saveBuildCache(path, cached); err != nil {
		t.Fatal(err)
	}
	got := InitializeClientBuild(path)
	if got.Number != cached.Number || got.Source != "cache" || commonProperties()["client_build_number"] != cached.Number {
		t.Fatalf("cached metadata not used in requests: %+v", got)
	}
	for _, bad := range []string{
		`not json`,
		`{"number":0}`,
		`{"number":9999999999}`,
		`{"number":1,"fetched_at":"2026-01-01T00:00:00Z"}`,
	} {
		os.WriteFile(path, []byte(bad), 0o600)
		if got := InitializeClientBuild(path); got.Number != ClientBuildNumber {
			t.Fatalf("invalid/stale cache used: %+v", got)
		}
	}
}

func TestRefreshClientBuild(t *testing.T) {
	isolateBuild(t)
	path := filepath.Join(t.TempDir(), "discord-build.json")
	number := ClientBuildNumber + 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" || r.Header.Get("User-Agent") == "" {
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]int{"BUILD_NUMBER": number})
	}))
	defer server.Close()
	info, err := refreshClientBuild(context.Background(), path, server.Client(), server.URL+"/login")
	if err != nil || info.Number != number || info.Source != "discord" {
		t.Fatalf("refresh: %+v %v", info, err)
	}
	currentBuild.Store(0)
	if got := InitializeClientBuild(path); got.Number != number {
		t.Fatalf("refresh was not cached: %+v", got)
	}
}

func TestBuildScriptFallback(t *testing.T) {
	isolateBuild(t)
	var externalHits atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalHits.Add(1)
		w.Write([]byte(`{"BUILD_NUMBER":999999}`))
	}))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`<script src="/assets/main.js"></script><script src="` + external.URL + `/assets/foreign.js"></script>`))
		case "/assets/main.js":
			w.Write([]byte(`{"BUILD_NUMBER":634304}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	info, err := refreshClientBuild(context.Background(), filepath.Join(t.TempDir(), "build.json"), server.Client(), server.URL+"/login")
	if err != nil || info.Number != 634304 || externalHits.Load() != 0 {
		t.Fatalf("script fallback: %+v %v external=%d", info, err, externalHits.Load())
	}
}

func TestBuildFailureKeepsCachedNumber(t *testing.T) {
	isolateBuild(t)
	currentBuild.Store(int64(ClientBuildNumber + 400))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	info, err := refreshClientBuild(context.Background(), filepath.Join(t.TempDir(), "build.json"), server.Client(), server.URL)
	if err == nil || info.Number != ClientBuildNumber+400 || CurrentClientBuildNumber() != info.Number {
		t.Fatalf("fallback: %+v %v", info, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := refreshClientBuild(ctx, "", server.Client(), server.URL); err == nil {
		t.Fatal("canceled refresh succeeded")
	}
}

func TestLiveDiscordBuild(t *testing.T) {
	if os.Getenv("LINKCORD_LIVE_DISCORD_BUILD") != "1" {
		t.Skip("LINKCORD_LIVE_DISCORD_BUILD is not set")
	}
	isolateBuild(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := RefreshClientBuild(ctx, filepath.Join(t.TempDir(), "build.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Discord web build: %d", info.Number)
}
