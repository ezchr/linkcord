// Package version provides the version embedded in both Linkcord entry points.
package version

// Version is overridden by release builds with -ldflags -X.
// It names the latest release, so a build from this tree is never replaced by
// an older one.
var Version = "v1.8.10"

func String() string {
	return Version
}
