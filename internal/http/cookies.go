//go:build !no_spoof_tls_fingerprint

package http

import (
	json "encoding/json/v2"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/ezchr/linkcord/internal/consts"
	"github.com/ezchr/linkcord/internal/tls"
)

// NewPersistentClient is like NewSessionClient, but its cookies are kept on
// disk, as a browser profile keeps them between starts: those Discord and
// Cloudflare set to last, not those for the session only.
func NewPersistentClient(token string) *api.Client {
	jar := newFileJar(cookiesPath())
	httpClient := httputil.NewClientWithDriver(wrapClient(tls.NewClient(tls_client.WithCookieJar(jar))))
	client := api.NewCustomClient(token, httpClient)
	client.UserAgent = BrowserUserAgent()
	return client
}

func cookiesPath() string {
	return filepath.Join(consts.CacheDir(), "cookies.json")
}

// discordURL is where the cookies kept are given back to the jar.
var discordURL = &url.URL{Scheme: "https", Host: "discord.com", Path: "/"}

// savedCookie is a cookie as kept on disk.
type savedCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
}

// fileJar is a cookie jar that writes Discord's lasting cookies to a file
// whenever they change.
type fileJar struct {
	tls_client.CookieJar
	path  string
	mu    sync.Mutex
	saved map[string]savedCookie
}

func newFileJar(path string) *fileJar {
	j := &fileJar{CookieJar: tls_client.NewCookieJar(), path: path, saved: make(map[string]savedCookie)}
	data, err := os.ReadFile(path)
	if err != nil {
		return j
	}
	var list []savedCookie
	if err := json.Unmarshal(data, &list); err != nil {
		slog.Warn("cookies file unreadable; starting without", "err", err)
		return j
	}
	now := time.Now()
	var cookies []*fhttp.Cookie
	for _, c := range list {
		if !c.Expires.After(now) {
			continue
		}
		j.saved[c.Domain+"|"+c.Path+"|"+c.Name] = c
		cookies = append(cookies, &fhttp.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: c.Expires, Secure: c.Secure, HttpOnly: c.HTTPOnly})
	}
	j.CookieJar.SetCookies(discordURL, cookies)
	return j
}

func (j *fileJar) SetCookies(u *url.URL, cookies []*fhttp.Cookie) {
	j.CookieJar.SetCookies(u, cookies)
	if !isDiscordHost(u.Hostname()) {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	changed := false
	now := time.Now()
	for _, c := range cookies {
		expires := c.Expires
		if c.MaxAge > 0 {
			expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		}
		domain := c.Domain
		if domain == "" {
			domain = u.Hostname()
		}
		key := domain + "|" + c.Path + "|" + c.Name
		if c.MaxAge < 0 || !expires.IsZero() && !expires.After(now) {
			// Deleted, or expired.
			if _, ok := j.saved[key]; ok {
				delete(j.saved, key)
				changed = true
			}
			continue
		}
		if expires.IsZero() {
			// A cookie for the session only goes with it, as in a browser.
			continue
		}
		j.saved[key] = savedCookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: expires, Secure: c.Secure, HTTPOnly: c.HttpOnly}
		changed = true
	}
	if changed {
		j.save()
	}
}

func (j *fileJar) save() {
	list := make([]savedCookie, 0, len(j.saved))
	for _, c := range j.saved {
		list = append(list, c)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(j.path), 0o700)
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		os.Rename(tmp, j.path)
	}
}

func isDiscordHost(host string) bool {
	return host == "discord.com" || strings.HasSuffix(host, ".discord.com")
}
