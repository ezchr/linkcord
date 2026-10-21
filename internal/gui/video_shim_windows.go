package gui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

// realExecutable is path itself, or for a Chocolatey shim (the small
// launcher Chocolatey's shimgen puts in its bin folder) the program it
// starts. A shim passes all input and output through itself: for a stream
// watched, every decoded picture would be copied once more for nothing.
func realExecutable(path string) string {
	if !strings.EqualFold(filepath.Base(filepath.Dir(path)), "bin") ||
		!strings.Contains(strings.ToLower(path), "chocolatey") {
		return path
	}
	shimMu.Lock()
	defer shimMu.Unlock()
	if t, ok := shimTargets[path]; ok {
		return t
	}
	target := shimTarget(path)
	shimTargets[path] = target
	return target
}

var (
	shimMu      sync.Mutex
	shimTargets = map[string]string{}
	shimLine    = regexp.MustCompile(`(?i)target:\s*'([^']+)'`)
)

func shimTarget(path string) string {
	cmd := exec.Command(path, "--shimgen-help")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, _ := cmd.Output()
	if m := shimLine.FindSubmatch(out); m != nil {
		t := string(m[1])
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(path), t)
		}
		if info, err := os.Stat(t); err == nil && !info.IsDir() {
			return t
		}
	}
	return path
}
