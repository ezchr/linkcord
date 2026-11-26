package gui

import (
	"net/http"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

// URLs are sent without query or fragment, digits as "#".
func TestSampledURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://discord.com/api/v9/channels/123/messages?limit=50": "https://discord.com/api/v#/channels/#/messages",
		"/users/@me/guilds#frag":                                    "/users/@me/guilds",
		"/guilds/1/members/22":                                      "/guilds/#/members/#",
	} {
		if got := sampledURL(in); got != want {
			t.Errorf("sampledURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Calls map to the web client's network_action events by method and route.
func TestNetworkAction(t *testing.T) {
	for _, c := range []struct{ method, route, want string }{
		{"POST", "/guilds/123/channels", "network_action_channel_create"},
		{"GET", "/guilds/123/channels", ""},
		{"GET", "/invites/abcDEF", "network_action_invite_resolve"},
		{"GET", "/invites/abc/extra", ""},
		{"POST", "/auth/logout", "network_action_user_logout"},
		{"POST", "/channels/1/messages", ""},
	} {
		if got := networkAction(c.method, c.route); got != c.want {
			t.Errorf("networkAction(%s %s) = %q, want %q", c.method, c.route, got, c.want)
		}
	}
}

func testScience() *science {
	return &science{last: map[string]time.Time{}, started: time.Now(), sessionID: "s", wake: make(chan struct{}, 1), userID: 5}
}

func TestRecordHTTP(t *testing.T) {
	resp := &httpdriver.MockResponse{StatusCode: http.StatusCreated}
	s := testScience()
	s.recordHTTPWith("POST", "/api/v9/guilds/12/channels", resp, 30*time.Millisecond, 0)
	if len(s.queue) != 2 {
		t.Fatalf("%d events queued", len(s.queue))
	}
	h := s.queue[0].Properties
	if s.queue[0].Type != "http_request" || h["url"] != "https://discord.com/api/v#/guilds/#/channels" || h["status_code"] != 201 || h["source"] != "sample" || h["method"] != "POST" {
		t.Fatalf("http_request: %v", h)
	}
	n := s.queue[1].Properties
	if s.queue[1].Type != "network_action_channel_create" || n["url"] != "/guilds/12/channels" || n["request_method"] != "POST" {
		t.Fatalf("network action: %v", n)
	}
	// Not sampled, and the analytics endpoints are never recorded.
	s = testScience()
	s.recordHTTPWith("GET", "/api/v9/users/@me", resp, 0, 0.5)
	s.recordHTTPWith("POST", "/api/v9/science", resp, 0, 0)
	if len(s.queue) != 0 {
		t.Fatalf("%d events queued", len(s.queue))
	}
	// Nothing while analytics are off.
	s.off = func() bool { return true }
	s.recordHTTPWith("POST", "/api/v9/guilds/12/channels", resp, 0, 0)
	if len(s.queue) != 0 {
		t.Fatal("recorded while turned off")
	}
}

func TestDeviceProps(t *testing.T) {
	s := testScience()
	s.track("app_opened", nil, "", 0)
	f, ok := s.queue[0].Properties["accessibility_features"].(int)
	if !ok || f&^(a11yReducedMotion|a11yHighContrast|a11yInvertColors|a11yPrefersLight|a11yPrefersDark|a11yForcedColors) != 0 {
		t.Fatalf("accessibility_features: %v", s.queue[0].Properties["accessibility_features"])
	}
}
