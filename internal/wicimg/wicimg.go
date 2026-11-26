// Package wicimg decodes pictures with the Windows Imaging Component, shrunk
// while they decode to the size they are shown at. Where it is not there
// (other systems, a build without cgo, a missing codec, or
// LINKCORD_GO_IMAGES=1), every call returns an error and the caller uses the
// Go decoders.
package wicimg

import (
	"errors"
	"os"
	"time"
)

// ErrUnavailable is returned where WIC is not used at all.
var ErrUnavailable = errors.New("wicimg: not available")

// disabled is set by LINKCORD_GO_IMAGES=1, to compare with or work around
// the Go decoders.
var disabled = os.Getenv("LINKCORD_GO_IMAGES") == "1"

// Fit is the size of a w×h picture shrunk to fit a square of size, never
// larger than it is. The short side is rounded up, so the picture is never
// drawn larger than what was decoded.
func Fit(w, h, size int) (int, int) {
	if size <= 0 || (w <= size && h <= size) {
		return max(w, 1), max(h, 1)
	}
	if w > h {
		return size, min(h, max((size*h+w-1)/w, 1))
	}
	return min(w, max((size*w+h-1)/h, 1)), size
}

// gifDelay is how long a GIF frame with delay d (hundredths of a second)
// shows: like browsers, 0 and 1 show for a tenth.
func gifDelay(d int) time.Duration {
	if d <= 1 {
		return 100 * time.Millisecond
	}
	return time.Duration(d) * 10 * time.Millisecond
}
