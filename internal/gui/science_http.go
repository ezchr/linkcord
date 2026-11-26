package gui

import (
	"math"
	mrand "math/rand/v2"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

// Accessibility feature bits of the web client's accessibility_features
// (web.js module 53298), for the OS media-query states it watches.
const (
	a11yReducedMotion = 2      // REDUCED_MOTION
	a11yHighContrast  = 8      // HIGH_CONTRAST, prefers-contrast: more
	a11yInvertColors  = 64     // INVERT_COLORS
	a11yPrefersLight  = 128    // PREFERS_COLOR_SCHEME_LIGHT
	a11yPrefersDark   = 256    // PREFERS_COLOR_SCHEME_DARK
	a11yForcedColors  = 262144 // FORCED_COLORS
)

// deviceProps caches accessibility_features and client_performance_cpu,
// read again at most every deviceRefresh.
var deviceProps struct {
	sync.Mutex
	at       time.Time
	features int
	cpu      float64
	cpuOK    bool
	sampler  cpuSampler
}

const deviceRefresh = 5 * time.Second

func currentDeviceProps() (features int, cpu float64, cpuOK bool) {
	d := &deviceProps
	d.Lock()
	defer d.Unlock()
	if d.at.IsZero() || time.Since(d.at) >= deviceRefresh {
		d.at = time.Now()
		d.features = systemAccessibilityFeatures()
		if v, ok := d.sampler.sample(); ok {
			d.cpu, d.cpuOK = math.Round(v*100)/100, true
		}
	}
	return d.features, d.cpu, d.cpuOK
}

// httpSampleRate is the share of REST calls sent as http_request events.
// The web client reads it from the remote config track_http_request, which
// Linkcord does not fetch; 1% is used.
const httpSampleRate = 0.01

var digitsRe = regexp.MustCompile(`\d+`)

// sampledURL is a request URL as the web client sends it in http_request:
// query and fragment removed, every run of digits replaced by "#".
func sampledURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return digitsRe.ReplaceAllString(u, "#")
}

// analyticsPath tells requests to the analytics endpoints, which are not
// themselves recorded.
var analyticsPath = regexp.MustCompile(`/api(/v\d+)?/(science|beaker)`)

var apiBase = func() *url.URL {
	u, _ := url.Parse(api.Endpoint)
	return u
}()

// networkActions are the API calls the web client declares
// trackedActionData for and Linkcord makes, by method and route with
// digits as "#"; "*" matches any one segment.
var networkActions = []struct {
	method, route, event string
}{
	{"POST", "/guilds/#/channels", "network_action_channel_create"},
	{"GET", "/invites/*", "network_action_invite_resolve"},
	{"POST", "/auth/logout", "network_action_user_logout"},
}

// networkAction is the event for an API call, if the web client tracks it.
// route is the path below /api/v9.
func networkAction(method, route string) string {
	r := strings.Split(strings.Trim(sampledURL(route), "/"), "/")
	for _, a := range networkActions {
		if a.method != method {
			continue
		}
		p := strings.Split(strings.Trim(a.route, "/"), "/")
		if len(p) != len(r) {
			continue
		}
		ok := true
		for i := range p {
			if p[i] != "*" && p[i] != r[i] {
				ok = false
				break
			}
		}
		if ok {
			return a.event
		}
	}
	return ""
}

// scienceHook binds a REST client to the science of its session, which is
// only made once READY gives the analytics token.
type scienceHook struct {
	sci   atomic.Pointer[science]
	start sync.Map // httpdriver.Request -> time.Time
}

var scienceHooks sync.Map // *httputil.Client -> *scienceHook

// hookScienceHTTP records client's calls for http_request and
// network_action_* events. It is called before the client is used, so the
// hook lists are not changed while requests read them.
func hookScienceHTTP(client *httputil.Client) {
	h := &scienceHook{}
	if _, loaded := scienceHooks.LoadOrStore(client, h); loaded {
		return
	}
	client.OnRequest = append(client.OnRequest, func(r httpdriver.Request) error {
		h.start.Store(r, time.Now())
		return nil
	})
	client.OnResponse = append(client.OnResponse, func(r httpdriver.Request, resp httpdriver.Response) error {
		v, ok := h.start.LoadAndDelete(r)
		if s := h.sci.Load(); s != nil && ok {
			s.recordHTTP(requestMethod(r), r.GetPath(), resp, time.Since(v.(time.Time)))
		}
		return nil
	})
}

// bindScienceHTTP points client's hook, if any, at s.
func bindScienceHTTP(client *httputil.Client, s *science) {
	if v, ok := scienceHooks.Load(client); ok {
		v.(*scienceHook).sci.Store(s)
	}
}

func requestMethod(r httpdriver.Request) string {
	if m, ok := r.(interface{ GetMethod() string }); ok {
		return m.GetMethod()
	}
	if d, ok := r.(*httpdriver.DefaultRequest); ok {
		return d.Method
	}
	return ""
}

// recordHTTP sends the sampled http_request event and the call's
// network_action_* event. resp is nil when the call got no response.
func (s *science) recordHTTP(method, path string, resp httpdriver.Response, took time.Duration) {
	s.recordHTTPWith(method, path, resp, took, mrand.Float64())
}

func (s *science) recordHTTPWith(method, path string, resp httpdriver.Response, took time.Duration, roll float64) {
	if s.off != nil && s.off() {
		return
	}
	if analyticsPath.MatchString(path) {
		return
	}
	full := apiBase.Scheme + "://" + apiBase.Host + path
	if roll < httpSampleRate {
		p := map[string]any{
			"url":         sampledURL(full),
			"method":      method,
			"duration_ms": took.Milliseconds(),
			"source":      "sample",
		}
		if resp != nil {
			p["status_code"] = resp.GetStatus()
		}
		s.track("http_request", p, "", 0)
	}
	route := strings.TrimPrefix(path, strings.TrimSuffix(apiBase.Path, "/"))
	if ev := networkAction(method, route); ev != "" && resp != nil {
		p := map[string]any{
			"status_code":    resp.GetStatus(),
			"url":            route,
			"request_method": method,
		}
		// error_code and error_message, which the web client takes from
		// the error body, are left out: reading the body here would take
		// it from the caller, which reads it after the hooks.
		s.track(ev, p, "", 0)
	}
}
