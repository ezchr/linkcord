//go:build !no_spoof_tls_fingerprint

package http

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestFileJarKeepsLastingCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	api := &url.URL{Scheme: "https", Host: "discord.com", Path: "/api/v9/users/@me"}
	j := newFileJar(path)
	j.SetCookies(api, []*fhttp.Cookie{
		{Name: "__dcfduid", Value: "a", Domain: ".discord.com", Path: "/", Expires: time.Now().Add(24 * time.Hour)},
		{Name: "__cfruid", Value: "b", Domain: ".discord.com", Path: "/"}, // for the session only
		{Name: "_cfuvid", Value: "c", Domain: ".discord.com", Path: "/", MaxAge: 3600},
	})
	j.SetCookies(&url.URL{Scheme: "https", Host: "example.com", Path: "/"}, []*fhttp.Cookie{
		{Name: "other", Value: "x", Expires: time.Now().Add(time.Hour)},
	})

	again := newFileJar(path)
	got := map[string]string{}
	for _, c := range again.Cookies(api) {
		got[c.Name] = c.Value
	}
	if got["__dcfduid"] != "a" || got["_cfuvid"] != "c" {
		t.Errorf("lasting cookies not kept: %v", got)
	}
	if _, ok := got["__cfruid"]; ok {
		t.Errorf("session cookie kept: %v", got)
	}
	if len(again.saved) != 2 {
		t.Errorf("kept %d cookies, want 2 (none of another site)", len(again.saved))
	}

	// Discord deleting one removes it from the file.
	again.SetCookies(api, []*fhttp.Cookie{{Name: "_cfuvid", Value: "", Domain: ".discord.com", Path: "/", MaxAge: -1}})
	if third := newFileJar(path); len(third.saved) != 1 {
		t.Errorf("deleted cookie still kept: %v", third.saved)
	}
}
