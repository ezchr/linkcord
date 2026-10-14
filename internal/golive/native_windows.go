//go:build windows && !no_voice

package golive

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/openh264 -O2
#cgo LDFLAGS: -lgdi32 -luser32 -lole32
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// loadCodec loads OpenH264 from path.
func loadCodec(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	switch r := C.lc_h264_load((*C.uint16_t)(unsafe.Pointer(p))); r {
	case 0:
		return nil
	case -1:
		return errors.New("golive: OpenH264 has no encoder")
	default:
		return fmt.Errorf("golive: could not load OpenH264: %w", windows.Errno(r))
	}
}

// encoder encodes pictures to H264.
type encoder struct {
	c    *C.lc_encoder
	w, h int
}

func newEncoder(w, h, fps, bitrate, idr int) (*encoder, error) {
	var cerr C.int
	c := C.lc_h264_open(C.int(w), C.int(h), C.int(fps), C.int(bitrate), C.int(idr), &cerr)
	if c == nil {
		return nil, fmt.Errorf("golive: could not start the H264 encoder (%d)", int(cerr))
	}
	return &encoder{c: c, w: w, h: h}, nil
}

// encode encodes the I420 picture, of the encoder's size, and returns a copy
// of the access unit, nil if the encoder skips it, and whether it is an IDR.
func (e *encoder) encode(i420 []byte, tsMs int64) ([]byte, bool, error) {
	var out *C.uint8_t
	var n, idr C.int
	if r := C.lc_h264_encode(e.c, (*C.uint8_t)(unsafe.Pointer(&i420[0])), C.int64_t(tsMs), &out, &n, &idr); r != 0 {
		return nil, false, fmt.Errorf("golive: H264 encoding failed (%d)", int(r))
	}
	if n == 0 {
		return nil, false, nil
	}
	return C.GoBytes(unsafe.Pointer(out), n), idr != 0, nil
}

func (e *encoder) forceIDR()              { C.lc_h264_force_idr(e.c) }
func (e *encoder) setBitrate(bitrate int) { C.lc_h264_set_bitrate(e.c, C.int(bitrate)) }
func (e *encoder) close()                 { C.lc_h264_close(e.c) }

// picture is what a capture grabbed: BGRA pixels in C memory, valid until
// the next grab.
type picture struct {
	pix          unsafe.Pointer
	w, h, stride int
}

// toI420 scales the picture to w by h into dst, in I420.
func (p picture) toI420(dst []byte, w, h int) {
	C.lc_bgra_to_i420((*C.uint8_t)(p.pix), C.int(p.w), C.int(p.h), C.int(p.stride), (*C.uint8_t)(unsafe.Pointer(&dst[0])), C.int(w), C.int(h))
}

// toRGBA scales the picture to w by h.
func (p picture) toRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	C.lc_bgra_to_rgba((*C.uint8_t)(p.pix), C.int(p.w), C.int(p.h), C.int(p.stride), (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(w), C.int(h))
	return img
}

// capturer captures a screen or a window.
type capturer struct {
	c *C.lc_capture
}

func newCapturer(src Source, cursor bool) *capturer {
	cur := C.int(0)
	if cursor {
		cur = 1
	}
	return &capturer{c: C.lc_capture_open(C.uintptr_t(src.Window), C.int(src.X), C.int(src.Y), C.int(src.W), C.int(src.H), cur)}
}

// grab captures a picture, ok false when there is none for now, and gone
// when the window was closed.
func (c *capturer) grab() (p picture, ok, gone bool) {
	var w, h, stride, g C.int
	pix := C.lc_capture_grab(c.c, &w, &h, &stride, &g)
	if pix == nil {
		return picture{}, false, g != 0
	}
	return picture{pix: unsafe.Pointer(pix), w: int(w), h: int(h), stride: int(stride)}, true, false
}

func (c *capturer) close() { C.lc_capture_close(c.c) }

// method is how the picture is captured, for the log.
func (c *capturer) method() string {
	if C.lc_capture_duplicates(c.c) != 0 {
		return "desktop duplication"
	}
	return "GDI"
}

// Sources returns the screens and the windows that can be shared, leaving
// out the window self, each with a picture of what it shows, thumbW pixels
// wide.
func Sources(self uintptr, thumbW int) []Source {
	const max = 64
	buf := (*[max]C.lc_source)(C.malloc(C.size_t(unsafe.Sizeof(C.lc_source{})) * max))
	defer C.free(unsafe.Pointer(buf))

	var out []Source
	n := int(C.lc_list_monitors(&buf[0], max))
	for i := range n {
		s := buf[i]
		out = append(out, Source{
			Kind: Screen, X: int(s.x), Y: int(s.y), W: int(s.w), H: int(s.h), Primary: s.primary != 0,
			Title: fmt.Sprintf("Screen %d", i+1),
		})
	}
	n = int(C.lc_list_windows(&buf[0], max, C.uintptr_t(self)))
	for i := range n {
		s := buf[i]
		title := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&s.title[0])), len(s.title)))
		out = append(out, Source{Kind: Window, Window: uintptr(s.hwnd), W: int(s.w), H: int(s.h), Title: title})
	}
	for i := range out {
		out[i].Thumb = thumbnail(out[i], thumbW)
	}
	return out
}

// thumbnail is a picture of what src shows, w pixels wide, or nil.
func thumbnail(src Source, w int) *image.RGBA {
	c := newCapturer(src, false)
	defer c.close()
	p, ok, _ := c.grab()
	if !ok || p.w == 0 || p.h == 0 {
		return nil
	}
	h := max(w*p.h/p.w, 1)
	return p.toRGBA(w, h)
}
