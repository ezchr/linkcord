// Package install keeps one copy of Linkcord, in
// %LocalAppData%\Programs\Linkcord, with a shortcut to it on the desktop, so
// that whichever copy is started, the latest one opens and updates go to
// the same place.
package install

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PortableEnvVar, set to 1, runs the copy started as it is, as tests do.
const PortableEnvVar = "LINKCORD_PORTABLE"

// Dir is where Linkcord is installed.
func Dir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserCacheDir()
	}
	return filepath.Join(base, "Programs", "Linkcord")
}

// Path is the installed executable.
func Path() string {
	return filepath.Join(Dir(), "linkcord.exe")
}

// Ensure makes the installed copy the latest of it and the one running, of
// version, and makes the desktop shortcut the first time. It reports
// whether it started the installed copy in place of this one, which should
// then exit. newer reports whether a version is newer than another.
func Ensure(version string, newer func(a, b string) (bool, error)) (relaunched bool) {
	if os.Getenv(PortableEnvVar) == "1" {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	target := Path()
	if samePath(self, target) {
		ensureShortcut(target)
		return false
	}

	installed := versionOf(target)
	if installed == "" || isNewer(newer, version, installed) {
		if err := copyFile(self, target); err != nil {
			slog.Warn("could not install Linkcord", "err", err, "to", target)
			if installed == "" {
				// Nowhere to go: this copy runs.
				return false
			}
		} else {
			slog.Info("installed Linkcord", "version", version, "to", target)
		}
	}
	ensureShortcut(target)

	cmd := exec.Command(target, os.Args[1:]...)
	cmd.Dir = Dir()
	if err := cmd.Start(); err != nil {
		slog.Warn("could not start the installed Linkcord", "err", err)
		return false
	}
	cmd.Process.Release()
	return true
}

func isNewer(newer func(a, b string) (bool, error), a, b string) bool {
	n, err := newer(a, b)
	return err == nil && n
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		a, b = ra, rb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// versionOf is the version of the executable at path, or "" if there is
// none or it does not say.
func versionOf(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// copyFile puts a copy of src at dst, through a temporary file so that a
// copy cut short never takes dst's place.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "linkcord-*.exe")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// ensureShortcut makes the desktop shortcut to target once, replacing one
// of the same name that pointed elsewhere: if the user deletes it, it stays
// deleted.
func ensureShortcut(target string) {
	marker := filepath.Join(Dir(), "shortcut-made")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	desktop := desktopDir()
	if desktop == "" {
		return
	}
	link := filepath.Join(desktop, "Linkcord.lnk")
	if err := makeShortcut(link, target); err != nil {
		slog.Warn("could not make the desktop shortcut", "err", err)
		return
	}
	os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0o600)
}

// desktopDir is the user's desktop folder.
func desktopDir() string {
	if p := os.Getenv("USERPROFILE"); p != "" {
		if d := filepath.Join(p, "Desktop"); isDir(d) {
			return d
		}
	}
	// The desktop may have been moved, as OneDrive does: ask Windows.
	out, err := powershell("[Environment]::GetFolderPath('Desktop')")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// makeShortcut writes a shortcut at link that starts target.
func makeShortcut(link, target string) error {
	script := "$s = (New-Object -ComObject WScript.Shell).CreateShortcut(" + psQuote(link) + ")\n" +
		"$s.TargetPath = " + psQuote(target) + "\n" +
		"$s.WorkingDirectory = " + psQuote(filepath.Dir(target)) + "\n" +
		"$s.IconLocation = " + psQuote(target+",0") + "\n" +
		"$s.Description = 'Linkcord'\n" +
		"$s.Save()"
	_, err := powershell(script)
	return err
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// powershell runs script hidden, with PowerShell 2, which Windows 7 has.
func powershell(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	hide(cmd)
	cmd.Stdin = strings.NewReader(script + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(strings.TrimSpace(string(out)) + ": " + err.Error())
	}
	return string(out), nil
}

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
