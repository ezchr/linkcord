//go:build !windows || !cgo

package wicimg

import (
	"image"
	"time"
)

// Still is not available here.
func Still(data []byte, size int) (*image.RGBA, error) { return nil, ErrUnavailable }

// GIF is not available here.
func GIF(data []byte, size, budget int) ([]image.Image, []time.Duration, error) {
	return nil, nil, ErrUnavailable
}
