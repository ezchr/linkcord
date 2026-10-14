package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxVideoDownload = 256 << 20

// mediaExecutable supports portable releases with tools beside linkcord.exe
// and development machines with FFmpeg installed on PATH.
func mediaExecutable(name string) (string, error) {
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	if executable, err := os.Executable(); err == nil {
		// Beside Linkcord, in its media folder, or where Linkcord downloads
		// it.
		for _, dir := range []string{filepath.Dir(executable), filepath.Join(filepath.Dir(executable), "media"), mediaDir()} {
			candidate := filepath.Join(dir, filename)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	path, err := exec.LookPath(filename)
	if err != nil {
		return "", fmt.Errorf("%s is required for video playback", filename)
	}
	return path, nil
}

// Download through Go's HTTP client. The decoder receives only a local file,
// never a remote URL or a URL containing command-line options.
func downloadVideo(ctx context.Context, client *http.Client, raw string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	resp, err := requestVideo(ctx, client, raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	file, err := os.CreateTemp("", "linkcord-video-*")
	if err != nil {
		return "", errors.New("could not create temporary video file")
	}
	path := file.Name()
	keep := false
	defer func() {
		file.Close()
		if !keep {
			os.Remove(path)
		}
	}()
	n, err := io.Copy(file, io.LimitReader(resp.Body, maxVideoDownload+1))
	if err != nil {
		return "", errors.New("video download was interrupted")
	}
	if n > maxVideoDownload {
		return "", errors.New("video exceeds the 256 MiB playback download limit")
	}
	if n == 0 {
		return "", errors.New("video file is empty")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("could not finish temporary video file")
	}
	keep = true
	return path, nil
}

// requestVideo starts the download of a video URL, refusing what is not one.
func requestVideo(ctx context.Context, client *http.Client, raw string) (*http.Response, error) {
	source, err := url.Parse(raw)
	if err != nil || source.Hostname() == "" || source.User != nil ||
		(source.Scheme != "https" && source.Scheme != "http") {
		return nil, errors.New("this embed does not provide a playable HTTP video URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("invalid video request")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not download video")
	}
	fail := func(err error) (*http.Response, error) {
		resp.Body.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("video server returned HTTP %d", resp.StatusCode))
	}
	if resp.ContentLength > maxVideoDownload {
		return fail(errors.New("video exceeds the 256 MiB playback download limit"))
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "application/json") {
		return fail(errors.New("this embed provides a web page instead of a video file"))
	}
	return resp, nil
}
