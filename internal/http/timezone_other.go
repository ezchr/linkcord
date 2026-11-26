//go:build !windows

package http

import (
	"os"
	"strings"
)

// systemTimeZone reads the zone /etc/localtime links to.
func systemTimeZone() string {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	if i := strings.Index(target, "zoneinfo/"); i >= 0 {
		if name := target[i+len("zoneinfo/"):]; validIANA(name) {
			return name
		}
	}
	return ""
}
