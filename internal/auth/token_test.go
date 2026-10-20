package auth

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestTokenRoundTrip(t *testing.T) {
	keyringService = "linkcord-test"
	legacyKeyringService = "linkcord-test-legacy"
	t.Cleanup(func() { DeleteToken() })

	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken on a missing token: %v", err)
	}
	if token, err := LoadToken(); err != nil || token != "" {
		t.Fatalf("LoadToken with nothing saved = %q, %v; want \"\", nil", token, err)
	}

	const want = "MTIzNDU2Nzg5MDEyMzQ1Njc4.GabcDE.abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	if err := SaveToken(want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if got, err := LoadToken(); err != nil || got != want {
		t.Fatalf("LoadToken = %q, %v; want %q", got, err, want)
	}

	// A refreshed token replaces the old one.
	const refreshed = want + "X"
	if err := SaveToken(refreshed); err != nil {
		t.Fatalf("SaveToken again: %v", err)
	}
	if got, _ := LoadToken(); got != refreshed {
		t.Fatalf("LoadToken after refresh = %q; want %q", got, refreshed)
	}

	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if got, _ := LoadToken(); got != "" {
		t.Fatalf("LoadToken after DeleteToken = %q; want \"\"", got)
	}
}

func TestCleanToken(t *testing.T) {
	for in, want := range map[string]string{
		"  abc.def  ": "abc.def",
		`"abc.def"`:   "abc.def",
		"'abc.def'\n": "abc.def",
	} {
		if got := CleanToken(in); got != want {
			t.Errorf("CleanToken(%q) = %q; want %q", in, got, want)
		}
	}
}

// The encrypted copy stands in when the Credential Manager has nothing, and
// is put back there; signing out removes both.
func TestTokenBackup(t *testing.T) {
	keyringService = "linkcord-test"
	legacyKeyringService = "linkcord-test-legacy"
	SetBackupDir(t.TempDir())
	t.Cleanup(func() { DeleteToken() })

	const want = "not-a-real-token: kept in the copy"
	if err := SaveToken(want); err != nil {
		t.Fatal(err)
	}
	// Lost from the Credential Manager only.
	keyring.Delete(keyringService, keyringUser)
	got, from, err := LoadTokenFrom()
	if err != nil || got != want || from != "backup" {
		t.Fatalf("LoadTokenFrom = %q, %q, %v; want the copy", got, from, err)
	}
	if got, from, _ := LoadTokenFrom(); got != want || from != "credentials" {
		t.Fatalf("not put back in the credentials: %q from %q", got, from)
	}
	if err := DeleteToken(); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := LoadTokenFrom(); got != "" {
		t.Fatalf("token %q left after signing out", got)
	}
}
