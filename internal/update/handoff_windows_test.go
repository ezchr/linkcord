package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the real Windows replacement helper against temporary executables,
// including paths with spaces/apostrophes and startup rollback. No running
// Linkcord instance or user-owned executable is involved.
func TestWindowsHandoff(t *testing.T) {
	if os.Getenv("LINKCORD_TEST_HANDOFF") != "1" {
		t.Skip("LINKCORD_TEST_HANDOFF is not set")
	}
	root := filepath.Join(t.TempDir(), "update user's test")
	os.MkdirAll(root, 0o700)
	source := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(source, []byte(`package main
import "os"
func main() {
	path := os.Getenv("LINKCORD_UPDATE_HEALTH_FILE")
	if path == "" { return }
	if os.Getenv("LINKCORD_TEST_UPDATE_FAIL") == "1" { os.Exit(13) }
	os.WriteFile(path, []byte(os.Getenv("LINKCORD_UPDATE_EXPECTED_VERSION")), 0600)
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "fixture.exe")
	cmd := exec.Command("go", "build", "-ldflags=-H=windowsgui", "-o", fixture, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v\n%s", err, output)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	for _, tc := range []struct {
		name            string
		failure, locked bool
	}{
		{"apply", false, false},
		{"rollback", true, false},
		{"locked", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, failure := tc.name, tc.failure
			dir, err := os.MkdirTemp(root, ".linkcord-update-")
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, name+" client's.exe")
			staged := filepath.Join(dir, "download.exe")
			old := append(append([]byte(nil), data...), []byte("previous-version")...)
			os.WriteFile(target, old, 0o700)
			if tc.locked {
				handle, err := syscall.CreateFile(syscall.StringToUTF16Ptr(target), syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer syscall.CloseHandle(handle)
			}
			os.WriteFile(staged, data, 0o700)
			failRecord := filepath.Join(root, name+"-failed-version")
			plan, err := createPlan(Plan{
				Dir: dir, Target: target, Staged: staged, Version: "v9.0.0",
				Digest: hex.EncodeToString(hash[:]), FailureRecord: failRecord,
				ParentPID: 0,
			})
			if err != nil {
				t.Fatal(err)
			}
			if failure {
				t.Setenv("LINKCORD_TEST_UPDATE_FAIL", "1")
			} else {
				t.Setenv("LINKCORD_TEST_UPDATE_FAIL", "0")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			if err := plan.Handoff(ctx); err != nil {
				log, _ := os.ReadFile(plan.Log + ".stderr")
				t.Fatalf("handoff: %v\n%s", err, log)
			}
			var result string
			for result == "" {
				if content, err := os.ReadFile(plan.Result); err == nil {
					result = strings.TrimSpace(string(content))
					break
				}
				select {
				case <-ctx.Done():
					log, _ := os.ReadFile(plan.Log)
					t.Fatalf("helper timeout: %s", log)
				case <-time.After(50 * time.Millisecond):
				}
			}
			got, _ := os.ReadFile(target)
			if tc.locked {
				if result != "failed" || string(got) != string(old) {
					t.Fatalf("locked target was changed: %s", result)
				}
				if _, err := os.Stat(failRecord); err == nil {
					t.Fatal("temporary file lock permanently blocked this release")
				}
			} else if failure {
				if result != "rolled-back" || string(got) != string(old) {
					log, _ := os.ReadFile(plan.Log)
					t.Fatalf("rollback failed: %s\n%s", result, log)
				}
				record, _ := os.ReadFile(failRecord)
				if !strings.HasPrefix(string(record), "v9.0.0") {
					t.Fatal("failed release was not recorded")
				}
			} else {
				if result != "applied" || string(got) != string(data) {
					t.Fatalf("replacement failed: %s", result)
				}
				backup, _ := os.ReadFile(plan.Backup)
				if string(backup) != string(old) {
					t.Fatal("previous executable was not backed up")
				}
			}
		})
	}
}
