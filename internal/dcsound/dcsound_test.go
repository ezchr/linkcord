package dcsound

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestGet downloads and decodes the sound of muting, when
// LINKCORD_LIVE_SOUNDS is set, as it reaches discord.com.
func TestGet(t *testing.T) {
	if os.Getenv("LINKCORD_LIVE_SOUNDS") == "" {
		t.Skip("set LINKCORD_LIVE_SOUNDS to reach discord.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	l := New(t.TempDir())
	for name := range assets {
		pcm, err := l.Get(ctx, name)
		if err != nil {
			t.Fatal(name, err)
		}
		t.Logf("%s: %.2f s", name, float64(len(pcm))/2/rate)
	}
}

// TestLook finds the sounds in Discord's web app, when LINKCORD_LIVE_SOUNDS
// is set.
func TestLook(t *testing.T) {
	if os.Getenv("LINKCORD_LIVE_SOUNDS") == "" {
		t.Skip("set LINKCORD_LIVE_SOUNDS to reach discord.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	l := New(t.TempDir())
	if err := l.lookLocked(ctx); err != nil {
		t.Fatal(err)
	}
	for name, file := range assets {
		if l.files[name] != file {
			t.Errorf("%s: the web app says %s, the table %s", name, l.files[name], file)
		}
	}
}

func TestResample(t *testing.T) {
	pcm := make([]int16, 44100*2)
	if got := len(resample(pcm, 44100)); got != 48000*2 {
		t.Errorf("a second at 44.1 kHz makes %d samples, not %d", got, 48000*2)
	}
}
