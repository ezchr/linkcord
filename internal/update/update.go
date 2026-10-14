// Package update checks GitHub releases and stages verified Windows updates.
package update

import (
	"log/slog"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	repository   = "ezchr/linkcord"
	maxAssetSize = 150 << 20
)

type Asset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type releaseCache struct {
	ETag    string  `json:"etag"`
	Release Release `json:"release"`
}

type Manager struct {
	Version  string
	client   *http.Client
	apiBase  string
	cacheDir string
	token    string
}

func DefaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "linkcord", "updates")
}

func NewManager(version, cacheDir string) *Manager {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir()
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many update redirects")
			}
			if len(via) != 0 {
				if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
					return errors.New("update redirect must use HTTPS")
				}
				if req.URL.Host != via[0].URL.Host {
					req.Header.Del("Authorization")
				}
			}
			return nil
		},
	}
	return &Manager{Version: version, cacheDir: cacheDir, client: client, apiBase: "https://api.github.com"}
}

// githubToken never uses Discord credentials. A token is not embedded,
// printed, or written into release metadata or the update helper.
func githubToken(ctx context.Context) string {
	if token := strings.TrimSpace(os.Getenv("LINKCORD_GITHUB_TOKEN")); token != "" {
		return token
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	hideCommand(cmd)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (m *Manager) request(ctx context.Context, path, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiBase+"/repos/"+repository+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "Linkcord/"+m.Version)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	return req, nil
}

func (m *Manager) Check(ctx context.Context) (*Release, error) {
	if m.token == "" {
		m.token = githubToken(ctx)
	}
	var cached releaseCache
	cachePath := filepath.Join(m.cacheDir, "release.json")
	if data, err := os.ReadFile(cachePath); err == nil && len(data) <= 2<<20 {
		_ = json.Unmarshal(data, &cached)
	}
	req, err := m.request(ctx, "/releases/latest", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	if cached.ETag != "" && cached.Release.Tag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var release Release
	switch resp.StatusCode {
	case http.StatusNotModified:
		if cached.Release.Tag == "" {
			return nil, errors.New("release cache is unavailable")
		}
		release = cached.Release
	case http.StatusOK:
		data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 2<<20 {
			return nil, errors.New("release metadata exceeds size limit")
		}
		if err := json.Unmarshal(data, &release); err != nil {
			return nil, errors.New("invalid release metadata")
		}
		if release.Draft || release.Prerelease {
			return nil, errors.New("latest release is not a stable published release")
		}
		if _, err := parseVersion(release.Tag); err != nil {
			return nil, errors.New("latest release has an invalid version tag")
		}
		if err := os.MkdirAll(m.cacheDir, 0o700); err == nil {
			data, _ := json.Marshal(releaseCache{ETag: resp.Header.Get("ETag"), Release: release})
			// Cache failure must not prevent a valid online check.
			_ = writeCache(cachePath, data)
		}
	default:
		if resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("release API returned 404: private repository access or a published release is required")
		}
		return nil, fmt.Errorf("release API returned HTTP %d", resp.StatusCode)
	}
	if release.Draft || release.Prerelease {
		return nil, errors.New("cached release is not stable")
	}
	if _, err := parseVersion(release.Tag); err != nil {
		return nil, errors.New("release version is invalid")
	}
	return &release, nil
}

func writeCache(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func releaseAsset(release *Release) (Asset, []byte, error) {
	for _, asset := range release.Assets {
		if asset.Name != "linkcord.exe" {
			continue
		}
		if asset.ID <= 0 || asset.Size <= 0 || asset.Size > maxAssetSize {
			return Asset{}, nil, errors.New("invalid Windows release asset")
		}
		if !strings.HasPrefix(asset.Digest, "sha256:") {
			return Asset{}, nil, errors.New("release asset is missing a SHA-256 digest")
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil || len(digest) != sha256.Size {
			return Asset{}, nil, errors.New("invalid release asset digest")
		}
		return asset, digest, nil
	}
	return Asset{}, nil, errors.New("release has no linkcord.exe asset")
}

type progressWriter struct {
	total, size int64
	last        int
	progress    func(int)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	percent := int(w.total * 100 / w.size)
	if percent != w.last && percent <= 100 {
		w.last = percent
		if w.progress != nil {
			w.progress(percent)
		}
	}
	return len(p), nil
}

// stagePrefix starts the name of the folder of each update: its download,
// the executable it replaced, and its log.
const stagePrefix = ".linkcord-update-"

// keepStages is how many of the latest updates keep their folder, for their
// logs and the executable to go back to.
const keepStages = 3

// stagingDir makes the folder to stage an update of target in: in the update
// cache, out of the executable's folder, where nothing cleans it up; or
// beside target if the cache is on another drive, since replacing a file
// needs both on one. Folders of older updates are removed, as are those that
// earlier versions left beside the executable.
func (m *Manager) stagingDir(target string) (string, error) {
	pruneStages(filepath.Dir(target), 0)
	dir := m.cacheDir
	if !strings.EqualFold(filepath.VolumeName(dir), filepath.VolumeName(target)) {
		dir = filepath.Dir(target)
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	pruneStages(dir, keepStages-1)
	return os.MkdirTemp(dir, stagePrefix)
}

// pruneStages removes the folders of finished updates in dir but the keep
// latest.
func pruneStages(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type stage struct {
		path string
		mod  time.Time
	}
	var stages []stage
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stagePrefix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		// One whose helper never wrote a result may still be in use, unless
		// it is old.
		if _, err := os.Stat(filepath.Join(path, "result")); err != nil && time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		stages = append(stages, stage{path, info.ModTime()})
	}
	slices.SortFunc(stages, func(a, b stage) int { return b.mod.Compare(a.mod) })
	for i, s := range stages {
		if i >= keep {
			os.RemoveAll(s.path)
		}
	}
}

// fetchFull downloads the release's executable to staged and verifies it.
func (m *Manager) fetchFull(ctx context.Context, asset Asset, expected []byte, staged string, progress func(int)) error {
	f, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	hash := sha256.New()
	counter := &progressWriter{size: asset.Size, last: -1, progress: progress}
	n, copyErr := m.download(ctx, asset, io.MultiWriter(f, hash, counter))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != asset.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected)) {
		return errors.New("download size or SHA-256 verification failed")
	}
	return nil
}

// download writes a release asset to w, at most its size.
func (m *Manager) download(ctx context.Context, asset Asset, w io.Writer) (int64, error) {
	req, err := m.request(ctx, "/releases/assets/"+strconv.FormatInt(asset.ID, 10), "application/octet-stream")
	if err != nil {
		return 0, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("asset download returned HTTP %d", resp.StatusCode)
	}
	return io.Copy(w, io.LimitReader(resp.Body, asset.Size+1))
}

// Prepare downloads the update into its own folder, on the executable's
// drive. The installed file is untouched until handoff.
func (m *Manager) Prepare(ctx context.Context, release *Release, target string, progress func(int)) (plan *Plan, err error) {
	newer, err := Newer(release.Tag, m.Version)
	if err != nil || !newer {
		return nil, errors.New("refusing to install a version that is not newer")
	}
	asset, expected, err := releaseAsset(release)
	if err != nil {
		return nil, err
	}
	failurePath := filepath.Join(m.cacheDir, "failed-version")
	if data, readErr := os.ReadFile(failurePath); readErr == nil {
		record := strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "\n")
		if record == release.Tag+"\n"+strings.TrimPrefix(asset.Digest, "sha256:") {
			return nil, errors.New("this release previously failed startup; keeping the installed version")
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	stageDir, err := m.stagingDir(target)
	if err != nil {
		return nil, fmt.Errorf("update folder is not writable: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(stageDir)
		}
	}()
	staged := filepath.Join(stageDir, "download.exe")
	// A patch from the version running is a small part of the whole file;
	// what it makes must still be the release's exact executable, or the
	// whole file is downloaded instead.
	if err := m.fetchPatch(ctx, release, target, asset, expected, staged, progress); err != nil {
		if !errors.Is(err, errNoPatch) {
			slog.Info("update patch unusable; downloading the whole executable", "err", err)
		}
		os.Remove(staged)
		if err := m.fetchFull(ctx, asset, expected, staged, progress); err != nil {
			return nil, err
		}
	}
	exe, err := pe.Open(staged)
	if err != nil {
		return nil, errors.New("download is not a Windows executable")
	}
	machine := exe.Machine
	_, executableHeader := exe.OptionalHeader.(*pe.OptionalHeader64)
	exe.Close()
	if machine != pe.IMAGE_FILE_MACHINE_AMD64 || !executableHeader {
		return nil, errors.New("release asset is not a Windows amd64 executable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return createPlan(Plan{
		Dir: stageDir, Target: target, Staged: staged, Version: release.Tag,
		Digest:        strings.TrimPrefix(asset.Digest, "sha256:"),
		FailureRecord: failurePath, ParentPID: os.Getpid(),
	})
}
