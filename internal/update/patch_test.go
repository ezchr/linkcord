package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gabstv/go-bsdiff/pkg/bsdiff"
)

// TestPreparePatch updates with a patch from the running version, falls back
// to the whole executable when the patch does not fit, and when there is
// none.
func TestPreparePatch(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("requires a Windows amd64 test executable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	newer, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	// The installed version: the new one with some bytes changed.
	older := append([]byte(nil), newer...)
	copy(older[len(older)/2:], "an older build of linkcord")
	patch, err := bsdiff.Bytes(older, newer)
	if err != nil {
		t.Fatal(err)
	}

	var fullHits, patchHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/123"):
			fullHits.Add(1)
			w.Write(newer)
		case strings.HasSuffix(r.URL.Path, "/124"):
			patchHits.Add(1)
			w.Write(patch)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	release := func(withPatch bool) *Release {
		r := testRelease(newer)
		if withPatch {
			sum := sha256.Sum256(patch)
			r.Assets = append(r.Assets, Asset{ID: 124, Name: PatchName("v1.4.0-dev"), Size: int64(len(patch)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
		}
		return r
	}
	prepare := func(installed []byte, withPatch bool) {
		t.Helper()
		m := testManager(t, server)
		target := filepath.Join(t.TempDir(), "linkcord.exe")
		os.WriteFile(target, installed, 0o700)
		plan, err := m.Prepare(context.Background(), release(withPatch), target, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(plan.Staged)
		if a, b := sha256.Sum256(got), sha256.Sum256(newer); a != b {
			t.Fatal("the staged executable is not the release's")
		}
		os.RemoveAll(plan.Dir)
	}

	// The patch fits the installed version: only it is downloaded.
	prepare(older, true)
	if patchHits.Load() != 1 || fullHits.Load() != 0 {
		t.Fatalf("patch downloads %d, whole downloads %d; want 1 and 0", patchHits.Load(), fullHits.Load())
	}
	// The installed version is some other build: the whole file follows.
	prepare([]byte("a local build"), true)
	if patchHits.Load() != 2 || fullHits.Load() != 1 {
		t.Fatalf("patch downloads %d, whole downloads %d; want 2 and 1", patchHits.Load(), fullHits.Load())
	}
	// No patch from this version: the whole file.
	prepare(older, false)
	if patchHits.Load() != 2 || fullHits.Load() != 2 {
		t.Fatalf("patch downloads %d, whole downloads %d; want 2 and 2", patchHits.Load(), fullHits.Load())
	}
}
