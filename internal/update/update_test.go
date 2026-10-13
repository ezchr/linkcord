package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testManager(t *testing.T, server *httptest.Server) *Manager {
	t.Helper()
	m := NewManager("v1.4.0-dev", t.TempDir())
	m.apiBase = server.URL
	m.token = "unit-test"
	return m
}

func testRelease(data []byte) *Release {
	hash := sha256.Sum256(data)
	return &Release{Tag: "v1.5.0", Assets: []Asset{{
		ID: 123, Name: "linkcord.exe", Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(hash[:]),
	}}}
}

func TestReleaseCheckAndETag(t *testing.T) {
	var hits atomic.Int32
	release := Release{Tag: "v1.5.0", Assets: []Asset{{ID: 123, Name: "linkcord.exe"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/ezchr/linkcord/releases/latest" || r.Header.Get("Authorization") != "Bearer unit-test" {
			t.Error("release request did not use the expected repository and credential")
		}
		if r.Header.Get("If-None-Match") == `"release-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"release-1"`)
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()
	m := testManager(t, server)
	for range 2 {
		got, err := m.Check(context.Background())
		if err != nil || got.Tag != release.Tag {
			t.Fatalf("release check: %+v %v", got, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatal("startup check did not revalidate the cached release")
	}
	cache, _ := os.ReadFile(filepath.Join(m.cacheDir, "release.json"))
	if strings.Contains(string(cache), "unit-test") {
		t.Fatal("credential was written into release metadata")
	}
}

func TestReleaseFailures(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v1.5.0","draft":true}`,
		`{"tag_name":"v1.5.0","prerelease":true}`,
		`{"tag_name":"unknown"}`,
		`invalid JSON`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer server.Close()
			if _, err := testManager(t, server).Check(context.Background()); err == nil {
				t.Fatal("invalid release accepted")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	if _, err := testManager(t, server).Check(context.Background()); err == nil {
		t.Fatal("private/inaccessible release was accepted")
	}
}

func TestAssetRequirements(t *testing.T) {
	for _, asset := range []Asset{
		{Name: "different.exe", ID: 1, Size: 10},
		{Name: "linkcord.exe", ID: 0, Size: 10},
		{Name: "linkcord.exe", ID: 1, Size: maxAssetSize + 1},
		{Name: "linkcord.exe", ID: 1, Size: 10, Digest: ""},
		{Name: "linkcord.exe", ID: 1, Size: 10, Digest: "sha256:123"},
	} {
		if _, _, err := releaseAsset(&Release{Assets: []Asset{asset}}); err == nil {
			t.Errorf("invalid asset accepted: %+v", asset)
		}
	}
}

func TestDownloadIntegrityAndRedirectCredentials(t *testing.T) {
	var leaked atomic.Bool
	data := []byte("a deliberately invalid executable")
	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.Write(data)
	}))
	defer assetServer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Error("download did not request asset bytes")
		}
		http.Redirect(w, r, assetServer.URL+"/asset", http.StatusFound)
	}))
	defer server.Close()
	m := testManager(t, server)
	target := filepath.Join(t.TempDir(), "linkcord.exe")
	os.WriteFile(target, []byte("installed version"), 0o600)
	release := testRelease(data)
	release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := m.Prepare(context.Background(), release, target, nil); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if leaked.Load() {
		t.Fatal("GitHub credential leaked to a download redirect")
	}
	original, _ := os.ReadFile(target)
	if string(original) != "installed version" {
		t.Fatal("failed download changed the installed executable")
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatal("failed download left a partial staging directory")
	}
	release = testRelease(data)
	if _, err := m.Prepare(context.Background(), release, target, nil); err == nil {
		t.Fatal("non-PE data with a valid digest accepted")
	}
}

func TestPrepareWindowsExecutable(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("requires a Windows amd64 test executable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()
	m := testManager(t, server)
	target := filepath.Join(t.TempDir(), "linkcord.exe")
	os.WriteFile(target, []byte("installed version"), 0o600)
	release := testRelease(data)
	plan, err := m.Prepare(context.Background(), release, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := os.ReadFile(plan.Script)
	if strings.Contains(string(script), "unit-test") || !strings.Contains(string(script), "File]::Replace") {
		t.Fatal("unsafe or incomplete update helper")
	}
	os.RemoveAll(plan.Dir)
	if err := os.MkdirAll(m.cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := release.Tag + "\n" + strings.TrimPrefix(release.Assets[0].Digest, "sha256:")
	os.WriteFile(filepath.Join(m.cacheDir, "failed-version"), []byte(record), 0o600)
	if _, err := m.Prepare(context.Background(), release, target, nil); err == nil {
		t.Fatal("previously failed release was retried automatically")
	}
}

func TestHealthyVersion(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), ".linkcord-update-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "healthy")
	t.Setenv("LINKCORD_UPDATE_HEALTH_FILE", path)
	t.Setenv("LINKCORD_UPDATE_EXPECTED_VERSION", "v1.5.0")
	if err := MarkHealthy("v1.4.0"); err == nil {
		t.Fatal("wrong version acknowledged")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("wrong version created a marker")
	}
	if err := MarkHealthy("v1.5.0"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v1.5.0" {
		t.Fatal("wrong health marker")
	}
}

func TestLiveGitHubRelease(t *testing.T) {
	if os.Getenv("LINKCORD_LIVE_GITHUB") != "1" {
		t.Skip("LINKCORD_LIVE_GITHUB is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m := NewManager("v1.4.0-dev", t.TempDir())
	release, err := m.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = releaseAsset(release)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := Newer(release.Tag, m.Version)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Latest GitHub release: %s; newer than %s: %v", release.Tag, m.Version, newer)
}
