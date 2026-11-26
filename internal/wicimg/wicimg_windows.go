//go:build windows && cgo

package wicimg

/*
#cgo CFLAGS: -O2
#cgo LDFLAGS: -lole32 -loleaut32
#include "wic.h"
*/
import "C"

import (
	"fmt"
	"image"
	"time"
	"unsafe"
)

func info(data []byte) (C.wic_info_t, error) {
	var in C.wic_info_t
	if disabled {
		return in, ErrUnavailable
	}
	if len(data) == 0 {
		return in, fmt.Errorf("wicimg: empty")
	}
	if hr := C.wic_info(unsafe.Pointer(&data[0]), C.size_t(len(data)), &in); hr < 0 {
		return in, fmt.Errorf("wicimg: open: hresult %#x", uint32(hr))
	}
	return in, nil
}

// Still decodes the picture in data (JPEG, PNG, GIF, BMP, ICO, TIFF, and
// WebP where Windows has its codec) shrunk to fit a square of size pixels,
// as premultiplied RGBA. A GIF gives its first frame, an icon its largest.
// EXIF orientation is not applied.
func Still(data []byte, size int) (*image.RGBA, error) {
	in, err := info(data)
	if err != nil {
		return nil, err
	}
	w, h := Fit(int(in.w), int(in.h), size)
	if w > C.WIC_MAX_OUT_SIDE || h > C.WIC_MAX_OUT_SIDE || int64(w)*int64(h) > C.WIC_MAX_OUT_PIXELS {
		return nil, fmt.Errorf("wicimg: %dx%d too large", w, h)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if hr := C.wic_still(unsafe.Pointer(&data[0]), C.size_t(len(data)), C.int(w), C.int(h), (*C.uint8_t)(unsafe.Pointer(&img.Pix[0]))); hr < 0 {
		return nil, fmt.Errorf("wicimg: decode: hresult %#x", uint32(hr))
	}
	return img, nil
}

// GIF decodes the frames of an animated GIF as they are seen, each drawn
// over the ones before as the GIF says, shrunk to fit a square of size.
// Frames past what budget bytes hold are merged into the ones kept, so the
// animation keeps its speed. A GIF of one frame returns no frames.
func GIF(data []byte, size, budget int) ([]image.Image, []time.Duration, error) {
	in, err := info(data)
	if err != nil {
		return nil, nil, err
	}
	if in.gif == 0 {
		return nil, nil, fmt.Errorf("wicimg: not a GIF")
	}
	n := int(in.frames)
	if n < 2 {
		return nil, nil, nil
	}
	if n > 100000 {
		return nil, nil, fmt.Errorf("wicimg: %d frames", n)
	}
	cw, ch := int(in.canvas_w), int(in.canvas_h)
	if cw <= 0 || ch <= 0 {
		cw, ch = int(in.w), int(in.h)
	}
	w, h := Fit(cw, ch, size)
	if w > C.WIC_MAX_OUT_SIDE || h > C.WIC_MAX_OUT_SIDE || int64(w)*int64(h) > C.WIC_MAX_OUT_PIXELS {
		return nil, nil, fmt.Errorf("wicimg: %dx%d too large", w, h)
	}
	keep := max(1, min(n, budget/max(w*h*4, 1)))
	step := (n + keep - 1) / keep
	keep = (n + step - 1) / step
	// One allocation holds every frame kept: they are let go together.
	pix := make([]byte, keep*w*h*4)
	cdelays := make([]C.int, n)
	kept := C.wic_gif(unsafe.Pointer(&data[0]), C.size_t(len(data)), C.int(cw), C.int(ch), C.int(w), C.int(h),
		C.int(step), C.int(keep), (*C.uint8_t)(unsafe.Pointer(&pix[0])), &cdelays[0], C.int(n))
	if kept < 0 {
		return nil, nil, fmt.Errorf("wicimg: gif: hresult %#x", uint32(kept))
	}
	if int(kept) != keep {
		return nil, nil, fmt.Errorf("wicimg: gif: %d of %d frames", kept, keep)
	}
	frames := make([]image.Image, keep)
	delays := make([]time.Duration, keep)
	for i := range frames {
		frames[i] = &image.RGBA{Pix: pix[i*w*h*4 : (i+1)*w*h*4 : (i+1)*w*h*4], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	}
	for i, d := range cdelays {
		delays[i/step] += gifDelay(int(d))
	}
	return frames, delays, nil
}
