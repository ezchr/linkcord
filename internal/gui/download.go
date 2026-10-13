package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconDownload = mustIcon(icons.FileFileDownload)
	iconDone     = mustIcon(icons.ActionCheckCircle)
)

// download is a file being saved to the Downloads folder.
type download struct {
	mu         sync.Mutex
	got, total int64
	path       string // where it was saved, once done
	err        error
	done       bool
}

func (d *download) state() (got, total int64, path string, err error, done bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.got, d.total, d.path, d.err, d.done
}

// downloadsDir is the user's Downloads folder.
func downloadsDir() string {
	if dir := knownDownloadsDir(); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, "Downloads")
}

// saveFile downloads raw into the Downloads folder as name, as Discord's app
// does, without the browser. A second click on a saved file shows it.
func (c *chatView) saveFile(raw, name string) *download {
	if c.downloads == nil {
		c.downloads = make(map[string]*download)
	}
	if d, ok := c.downloads[raw]; ok {
		if _, _, p, err, done := d.state(); done && err == nil {
			showInFolder(p)
			return d
		} else if !done {
			return d
		}
	}
	d := &download{total: -1}
	c.downloads[raw] = d
	u := c.u
	go func() {
		p, err := fetchToDownloads(raw, name, d, u)
		d.mu.Lock()
		d.path, d.err, d.done = p, err, true
		d.mu.Unlock()
		if err != nil {
			slog.Warn("download failed", "url", raw, "err", err)
		}
		u.win.Invalidate()
	}()
	return d
}

func fetchToDownloads(raw, name string, d *download, u *ui) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("not a web address")
	}
	if name == "" {
		name = path.Base(parsed.Path)
	}
	name = safeFileName(name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	d.mu.Lock()
	d.total = resp.ContentLength
	d.mu.Unlock()

	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Written under a temporary name, so that a broken download never looks
	// finished.
	tmp, err := os.CreateTemp(dir, ".linkcord-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	buf := make([]byte, 128<<10)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return "", err
			}
			d.mu.Lock()
			d.got += int64(n)
			d.mu.Unlock()
			if time.Since(last) > 100*time.Millisecond {
				last = time.Now()
				u.win.Invalidate()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			return "", rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	target := freeName(filepath.Join(dir, name))
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}

// safeFileName keeps a name Windows accepts.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" {
		name = "download"
	}
	return name
}

// freeName is p, or p with " (2)", " (3)"… before its extension when taken.
func freeName(p string) string {
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		q := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(q); err != nil {
			return q
		}
	}
}
