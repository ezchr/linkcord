package http

import (
	"os"
	"strings"
	"sync"
	"time"
)

// TimeZone is the IANA name of the local time zone, as the web client sends
// it in X-Discord-Timezone (Intl.DateTimeFormat().resolvedOptions().timeZone),
// or "" when it cannot be told, when the header is left out.
var TimeZone = sync.OnceValue(func() string {
	if tz := os.Getenv("TZ"); validIANA(tz) {
		return tz
	}
	if name := time.Local.String(); validIANA(name) {
		return name
	}
	return systemTimeZone()
})

// validIANA says whether name looks like an IANA zone name rather than Go's
// "Local" placeholder or a POSIX rule.
func validIANA(name string) bool {
	if name == "" || name == "Local" || strings.HasPrefix(name, ":") {
		return false
	}
	if name == "UTC" {
		return true
	}
	return strings.Contains(name, "/")
}

// windowsToIANA maps a Windows time zone key name to the IANA zone of its
// territory "001", as CLDR's windowsZones.xml does.
func windowsToIANA(key string) string {
	return windowsZones[strings.TrimSpace(key)]
}
