// Package version provides the version embedded in both Linkcord entry points.
package version

// Version is overridden by release builds with -ldflags -X.
// Development builds must not identify themselves as an older stable release.
var Version = "v1.4.4-dev"

func String() string {
	return Version
}
