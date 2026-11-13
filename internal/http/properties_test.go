package http

import (
	"context"
	"io"
	"maps"
	nethttp "net/http"
	"net/url"
	"strings"
	"testing"
	"uuid"

	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ezchr/linkcord/internal/tls"
)

func TestCommonProperties(t *testing.T) {
	properties := commonProperties()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"properties browser", properties[gateway.IdentifyBrowser], tls.BrowserName()},
		{"properties version", properties["browser_version"], tls.BrowserVersion()},
		{"properties user agent", properties["browser_user_agent"], BrowserUserAgent()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got = %v, want = %v", test.got, test.want)
			}
		})
	}
}

func TestBrowserUserAgent(t *testing.T) {
	if product := tls.BrowserName() + "/" + tls.BrowserVersion(); !strings.Contains(BrowserUserAgent(), product) {
		t.Fatalf("user agent does not contain %q", product)
	}
}

func TestHeaders(t *testing.T) {
	headers := Headers()
	version := tls.BrowserMajorVersion()
	tests := []struct {
		name string
		want string
	}{
		{"Sec-Ch-Ua", `"Chromium";v="` + version + `", "Not-A.Brand";v="24", "Google Chrome";v="` + version + `"`},
		{"Sec-Ch-Ua-Mobile", "?0"},
		{"Sec-Ch-Ua-Platform", `"Windows"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := headers.Get(test.name); got != test.want {
				t.Fatalf("got = %q, want = %q", got, test.want)
			}
		})
	}
}

func TestXSuperProperties(t *testing.T) {
	properties := xSuperProperties()
	for _, key := range []gateway.IdentifyPropertyKey{"client_launch_id", "client_heartbeat_session_id", "launch_signature"} {
		t.Run(string(key), func(t *testing.T) {
			value, ok := properties[key].(string)
			if !ok {
				t.Fatalf("got %T, want string", properties[key])
			}
			id, err := uuid.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			if id[6]>>4 != 4 || id[8]>>6 != 2 {
				t.Errorf("got non-v4 UUID %q", id)
			}
		})
	}
}

func TestOneLaunchIDPerRun(t *testing.T) {
	a, b := IdentifyProperties()["client_launch_id"], xSuperProperties()["client_launch_id"]
	if a != b || a == "" {
		t.Fatalf("identify %v and super properties %v launch ids differ", a, b)
	}
	if _, ok := xSuperProperties()["device_model_name"]; !ok {
		t.Fatal("device_model_name missing")
	}
}

// fakeRequest is a request with a method, as the TLS client's are.
type fakeRequest struct {
	method string
	h      nethttp.Header
}

func (r *fakeRequest) GetMethod() string           { return r.method }
func (r *fakeRequest) GetPath() string             { return "/api/v9/x" }
func (r *fakeRequest) GetContext() context.Context { return context.Background() }
func (r *fakeRequest) AddHeader(h nethttp.Header)  { maps.Copy(r.h, h) }
func (r *fakeRequest) AddQuery(url.Values)         {}
func (r *fakeRequest) WithBody(io.ReadCloser)      {}

func TestPageHeaders(t *testing.T) {
	opt := PageHeaders(func() string { return "https://discord.com/channels/1/2" })
	for _, tc := range []struct {
		method string
		origin bool
	}{{"GET", false}, {"HEAD", false}, {"POST", true}, {"PATCH", true}, {"DELETE", true}} {
		r := &fakeRequest{method: tc.method, h: nethttp.Header{}}
		if err := opt(r); err != nil {
			t.Fatal(err)
		}
		if got := r.h.Get("Referer"); got != "https://discord.com/channels/1/2" {
			t.Errorf("%s Referer %q", tc.method, got)
		}
		if has := r.h.Get("Origin") != ""; has != tc.origin {
			t.Errorf("%s Origin sent: %v", tc.method, has)
		}
	}
}
