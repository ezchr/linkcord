package gui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ezchr/linkcord/internal/version"
)

// freezeAfter is how long work posted to the window may wait before the
// window counts as frozen.
const freezeAfter = 20 * time.Second

// startFreezeWatchdog writes what every goroutine is doing to freeze.txt,
// beside the log, when the window stops running what is posted to it, so
// that a window that hangs, as one did on Windows 7, tells where.
func (u *ui) startFreezeWatchdog() func() {
	done := make(chan struct{})
	go func() {
		const every = 5 * time.Second
		t := time.NewTicker(every)
		defer t.Stop()
		last := time.Now()
		var dumped time.Time
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				// A tick late by much means the computer slept, which is no
				// freeze of the window.
				slept := now.Sub(last) > 2*every
				last = now
				u.mu.Lock()
				since := u.pendingSince
				u.mu.Unlock()
				if slept || since.IsZero() || now.Sub(since) < freezeAfter || since.Equal(dumped) {
					continue
				}
				dumped = since
				dumpFreeze(now.Sub(since))
				// Sent at once, as the window may never answer again.
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					sendReports(ctx)
				}()
			}
		}
	}()
	return func() { close(done) }
}

// dumpFreeze writes the stacks of every goroutine to freeze.txt.
func dumpFreeze(waited time.Duration) {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	path := filepath.Join(filepath.Dir(logPath()), "freeze.txt")
	slog.Error("the window stopped responding; what it was doing is in freeze.txt", "waited", waited.Round(time.Second))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- %s froze for %s at %s on %s\n%s", version.String(), waited.Round(time.Second), time.Now().Format(time.RFC3339), runtime.GOOS, buf)
}
