package gui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDownloadMediaManual downloads FFmpeg for real, only when asked.
func TestDownloadMediaManual(t *testing.T) {
	if os.Getenv("LINKCORD_FETCH_TEST") == "" {
		t.Skip("downloads FFmpeg; set LINKCORD_FETCH_TEST=1")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	mediaNotify = func(s string) { t.Log(s) }
	if err := downloadMedia(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if _, err := os.Stat(filepath.Join(mediaDir(), name)); err != nil {
			t.Error(err)
		}
	}
	entries, _ := os.ReadDir(mediaDir())
	for _, e := range entries {
		t.Log(e.Name())
	}
}
