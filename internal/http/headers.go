package http

import (
	"encoding/base64"
	json "encoding/json/v2"
	"log/slog"
	stdHttp "net/http"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

func Headers() stdHttp.Header {
	headers := make(stdHttp.Header)
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers
	headers.Set("Accept", "*/*")
	headers.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Origin", api.BaseEndpoint)
	headers.Set("Priority", "u=1, i")
	headers.Set("Referer", "https://discord.com/channels/@me")

	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-origin")

	headers.Set("Sec-Ch-Ua", browserBrands())
	headers.Set("Sec-Ch-Ua-Mobile", "?0")
	headers.Set("Sec-Ch-Ua-Platform", `"`+osName+`"`)

	headers.Set("X-Debug-Options", "bugReporterEnabled")
	headers.Set("X-Discord-Locale", string(locale))

	superProps, err := json.Marshal(xSuperProperties())
	if err != nil {
		slog.Error("failed to marshal super props", "err", err)
	} else {
		headers.Set("X-Super-Properties", base64.StdEncoding.EncodeToString(superProps))
	}

	return headers
}

// PageHeaders are Headers as a browser tab sends them with each request:
// Referer is the page shown (page gives it, "" for the default), and GET and
// HEAD requests carry no Origin, which browsers send only with requests
// that change something.
func PageHeaders(page func() string) httputil.RequestOption {
	base := Headers()
	return func(r httpdriver.Request) error {
		h := base.Clone()
		if p := page(); p != "" {
			h.Set("Referer", p)
		}
		if m, ok := r.(interface{ GetMethod() string }); ok {
			if method := m.GetMethod(); method == stdHttp.MethodGet || method == stdHttp.MethodHead {
				h.Del("Origin")
			}
		}
		r.AddHeader(h)
		return nil
	}
}
