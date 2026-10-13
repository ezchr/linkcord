package gui

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// Optional real-provider smoke tests; ordinary tests remain offline.
func TestLiveMedia(t *testing.T) {
	tenor, apng := os.Getenv("LINKCORD_LIVE_TENOR"), os.Getenv("LINKCORD_LIVE_APNG")
	if tenor == "" && apng == "" {
		t.Skip("LINKCORD_LIVE_TENOR and LINKCORD_LIVE_APNG are not set")
	}
	for _, tc := range []struct{ name, source string }{{"tenor", tenor}, {"apng", apng}} {
		if tc.source == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source
			if tc.name == "tenor" {
				source = tenorGIFURL(source)
				if source == "" {
					t.Fatal("unrecognized Tenor media URL")
				}
			}
			im := &images{
				client: &http.Client{Timeout: 30 * time.Second},
				sem:    make(chan struct{}, 1), dir: t.TempDir(),
			}
			frames, delays, _, err := im.download(tc.name, source, 320)
			if err != nil || len(frames) < 2 || len(frames) != len(delays) {
				t.Fatalf("frames=%d delays=%d err=%v", len(frames), len(delays), err)
			}
			cached, _, err := im.animFromDisk(tc.name, 320)
			if err != nil || len(cached) != len(frames) {
				t.Fatalf("cached frames=%d err=%v", len(cached), err)
			}
			t.Logf("%s: %d animated frames, cached successfully", source, len(frames))
		})
	}
}
