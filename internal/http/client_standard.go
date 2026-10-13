//go:build no_spoof_tls_fingerprint

package http

import (
	"net/http"
	"net/http/cookiejar"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

func NewClient(token string) *api.Client {
	stdClient := http.Client{Transport: newTransport()}
	httpClient := httputil.NewClientWithDriver(httpdriver.WrapClient(stdClient))
	client := api.NewCustomClient(token, httpClient)
	client.UserAgent = BrowserUserAgent()
	return client
}

// NewSessionClient is like NewClient, but keeps the cookies that Discord sets
// across requests, as a browser tab does.
func NewSessionClient(token string) *api.Client {
	jar, _ := cookiejar.New(nil)
	stdClient := http.Client{Transport: newTransport(), Jar: jar}
	httpClient := httputil.NewClientWithDriver(httpdriver.WrapClient(stdClient))
	client := api.NewCustomClient(token, httpClient)
	client.UserAgent = BrowserUserAgent()
	return client
}
