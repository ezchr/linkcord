package gui

import (
	"os"
	"testing"
)

// TestGoogleTranslate asks Google to translate. Set LINKCORD_TRANSLATE_TEST=1
// to run it.
func TestGoogleTranslate(t *testing.T) {
	if os.Getenv("LINKCORD_TRANSLATE_TEST") == "" {
		t.Skip("set LINKCORD_TRANSLATE_TEST=1 to ask Google")
	}
	out, from, err := googleTranslate("the logs from yesterday. See you tomorrow!", "es")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("from %s: %s", from, out)
	if from != "en" || out == "" {
		t.Fatalf("from %q, %q", from, out)
	}
}
