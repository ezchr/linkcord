package gui

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FFmpeg plays videos, voice messages and watched streams. Most people do
// not have it, so Linkcord downloads it once, the first time it is needed,
// into its own folder, from these builds, the first that works.
var mediaSources = []string{
	"https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl-shared.zip",
	"https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip",
}

// mediaDir is where the FFmpeg Linkcord downloads is kept.
func mediaDir() string { return filepath.Join(filepath.Dir(logPath()), "media") }

// mediaFetch is the download of FFmpeg, done once at a time.
var mediaFetch struct {
	mu   sync.Mutex
	done chan struct{}
	err  error
}

// mediaNotify tells the user how the download goes, set by the window.
var mediaNotify = func(string) {}

// fetchMedia starts downloading FFmpeg, unless it is already, and returns
// what is closed once it is done, with mediaFetchErr saying how it went.
func fetchMedia() <-chan struct{} {
	m := &mediaFetch
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done != nil {
		select {
		case <-m.done:
			// A failed download is tried again; one that worked is not
			// needed again.
			if m.err == nil {
				return m.done
			}
		default:
			return m.done
		}
	}
	m.done = make(chan struct{})
	done := m.done
	go func() {
		err := downloadMedia()
		m.mu.Lock()
		m.err = err
		m.mu.Unlock()
		close(done)
	}()
	return done
}

func mediaFetchErr() error {
	mediaFetch.mu.Lock()
	defer mediaFetch.mu.Unlock()
	return mediaFetch.err
}

// needMedia finds the program name of FFmpeg, downloading FFmpeg first if
// it is missing, which may take a while.
func needMedia(ctx context.Context, name string) (string, error) {
	p, err := mediaExecutable(name)
	if err == nil || runtime.GOOS != "windows" {
		return p, err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-fetchMedia():
	}
	if err := mediaFetchErr(); err != nil {
		return "", err
	}
	return mediaExecutable(name)
}

// downloadMedia downloads FFmpeg from the first source that works, and
// checks that it runs here.
func downloadMedia() error {
	var errs []error
	for _, src := range mediaSources {
		err := downloadMediaFrom(src)
		if err == nil {
			mediaNotify("FFmpeg is ready")
			return nil
		}
		slog.Warn("could not get FFmpeg", "source", src, "err", err)
		errs = append(errs, err)
	}
	mediaNotify("Could not download FFmpeg; see the logs")
	return fmt.Errorf("could not download FFmpeg: %w", errors.Join(errs...))
}

func downloadMediaFrom(src string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", src, resp.Status)
	}
	tmp, err := os.CreateTemp("", "linkcord-ffmpeg-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	total := resp.ContentLength
	mediaNotify(fmt.Sprintf("Downloading FFmpeg, needed for videos and streams, once (%d MB)…", max(total, 0)>>20))
	var got int64
	last := 0
	buf := make([]byte, 256<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				return err
			}
			got += int64(n)
			if total > 0 {
				if pct := int(got * 100 / total); pct/20 > last/20 {
					last = pct
					mediaNotify(fmt.Sprintf("Downloading FFmpeg… %d%%", pct))
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	// Its programs and their libraries go in a folder of their own, which
	// takes the place of the old one once complete.
	dir := mediaDir()
	part := dir + ".part"
	os.RemoveAll(part)
	if err := os.MkdirAll(part, 0o755); err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return err
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		name := path.Base(f.Name)
		if !strings.Contains(f.Name, "/bin/") || name == "ffplay.exe" || f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(name, ".exe") && !strings.HasSuffix(name, ".dll") {
			continue
		}
		if err := extractFile(f, filepath.Join(part, name)); err != nil {
			return err
		}
		found = found || name == "ffmpeg.exe"
	}
	if !found {
		return errors.New("the download had no ffmpeg.exe")
	}
	// It must run on this Windows, which old ones may not.
	cmd := exec.Command(filepath.Join(part, "ffmpeg.exe"), "-version")
	hideMediaProcess(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("FFmpeg does not run here: %v %s", err, firstLine(string(out)))
	}
	os.RemoveAll(dir)
	return os.Rename(part, dir)
}

func extractFile(f *zip.File, dst string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
