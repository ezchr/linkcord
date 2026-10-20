package gui

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ezchr/linkcord/internal/auth"
)

// loadSavedToken reads the token saved at the last sign-in. Right after
// Windows starts, as when Linkcord starts with it, the Credential Manager
// may fail for a while: the encrypted copy kept beside the log stands in,
// and without one it is asked again for up to a minute rather than showing
// the sign-in.
func loadSavedToken() string {
	auth.SetBackupDir(filepath.Dir(logPath()))
	deadline := time.Now().Add(time.Minute)
	for try := 0; ; try++ {
		token, from, err := auth.LoadTokenFrom()
		if token != "" {
			slog.Info("signed in with the saved token", "from", from, "tries", try+1)
			auth.KeepBackup(token)
			return token
		}
		if err == nil {
			slog.Info("no saved token: signing in")
			return ""
		}
		if time.Now().After(deadline) {
			slog.Error("could not read the saved token; signing in", "err", err, "tries", try+1)
			return ""
		}
		if try%10 == 0 {
			slog.Warn("the saved token cannot be read yet", "err", err, "try", try)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
