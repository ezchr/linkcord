package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// BuildInfo describes the web-build metadata used for Discord requests.
type BuildInfo struct {
	Number    int       `json:"number"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"-"`
}

var currentBuild atomic.Int64

var (
	buildPattern  = regexp.MustCompile(`(?i)(?:"BUILD_NUMBER"|"buildNumber"|'BUILD_NUMBER'|'buildNumber'|\bbuildNumber)\s*[:=]\s*["']?([0-9]{1,9})\b`)
	scriptPattern = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
)

// CurrentClientBuildNumber always returns usable metadata, even offline.
func CurrentClientBuildNumber() int {
	if n := currentBuild.Load(); n > 0 {
		return int(n)
	}
	return ClientBuildNumber
}

// InitializeClientBuild loads a previously verified build before any session
// is created. An older cache never overrides a newer built-in build.
func InitializeClientBuild(cachePath string) BuildInfo {
	info := BuildInfo{Number: ClientBuildNumber, Source: "builtin"}
	data, err := os.ReadFile(cachePath)
	if err == nil && len(data) <= 8192 {
		var cached BuildInfo
		if json.Unmarshal(data, &cached) == nil && validBuild(cached.Number) &&
			cached.Number >= ClientBuildNumber && !cached.FetchedAt.IsZero() &&
			cached.FetchedAt.Before(time.Now().Add(24*time.Hour)) {
			cached.Source = "cache"
			info = cached
		}
	}
	currentBuild.Store(int64(info.Number))
	return info
}

func validBuild(n int) bool {
	return n > 0 && n <= 999999999
}

func parseBuild(data []byte) (int, bool) {
	for _, match := range buildPattern.FindAllSubmatch(data, -1) {
		n, err := strconv.Atoi(string(match[1]))
		if err == nil && validBuild(n) {
			return n, true
		}
	}
	return 0, false
}

// RefreshClientBuild refreshes metadata only; API/gateway behavior remains
// part of the installed executable.
func RefreshClientBuild(ctx context.Context, cachePath string) (BuildInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	return refreshClientBuild(ctx, cachePath, client, "https://discord.com/login")
}

func fetchBuildDocument(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", BrowserUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Discord metadata request returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("Discord metadata document exceeds size limit")
	}
	return data, err
}

func refreshClientBuild(ctx context.Context, cachePath string, client *http.Client, loginURL string) (BuildInfo, error) {
	fallback := BuildInfo{Number: CurrentClientBuildNumber(), Source: "fallback"}
	page, err := fetchBuildDocument(ctx, client, loginURL, 2<<20)
	if err != nil {
		return fallback, err
	}
	number, found := parseBuild(page)
	if !found {
		// Some deployments put metadata in a script rather than the HTML.
		// Check only the last four same-origin asset scripts, never arbitrary
		// URLs found in the page.
		base, err := url.Parse(loginURL)
		if err != nil {
			return fallback, err
		}
		scripts := scriptPattern.FindAllSubmatch(page, -1)
		checked := 0
		for i := len(scripts) - 1; i >= 0 && checked < 4; i-- {
			ref, err := url.Parse(html.UnescapeString(string(scripts[i][1])))
			if err != nil {
				continue
			}
			target := base.ResolveReference(ref)
			if target.Scheme != base.Scheme || target.Host != base.Host || !strings.HasPrefix(target.Path, "/assets/") || !strings.HasSuffix(target.Path, ".js") {
				continue
			}
			checked++
			data, err := fetchBuildDocument(ctx, client, target.String(), 8<<20)
			if err != nil {
				if ctx.Err() != nil {
					return fallback, ctx.Err()
				}
				continue
			}
			if number, found = parseBuild(data); found {
				break
			}
		}
	}
	if !found {
		return fallback, errors.New("Discord web-build number was not found")
	}
	if err := ctx.Err(); err != nil {
		return fallback, err
	}
	// A stale CDN response must not roll metadata back.
	if number < CurrentClientBuildNumber() {
		return fallback, errors.New("Discord returned an older web build")
	}
	info := BuildInfo{Number: number, FetchedAt: time.Now().UTC(), Source: "discord"}
	currentBuild.Store(int64(number))
	if err := saveBuildCache(cachePath, info); err != nil {
		return info, fmt.Errorf("web build refreshed but cache could not be saved: %w", err)
	}
	return info, nil
}

func saveBuildCache(cachePath string, info BuildInfo) error {
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".discord-build-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := json.NewEncoder(f).Encode(info); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, cachePath)
}
