package gui

import (
	"os"
	"strings"
	"testing"
)

// The Chocolatey ffmpeg shim, where this computer has one, resolves to the
// program it launches.
func TestRealExecutableShim(t *testing.T) {
	shim := `C:\ProgramData\chocolatey\bin\ffmpeg.exe`
	if _, err := os.Stat(shim); err != nil {
		t.Skip("no Chocolatey ffmpeg here")
	}
	got := realExecutable(shim)
	if got == shim || !strings.Contains(strings.ToLower(got), `chocolatey\lib`) {
		t.Fatalf("realExecutable(%q) = %q", shim, got)
	}
	if p := `C:\Windows\System32\cmd.exe`; realExecutable(p) != p {
		t.Fatal("a normal program changed")
	}
}
