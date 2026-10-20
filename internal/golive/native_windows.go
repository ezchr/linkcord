//go:build windows && !no_voice

package golive

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/openh264 -O2
#cgo LDFLAGS: -lgdi32 -luser32 -lole32 -loleaut32 -lmfuuid -lstrmiids
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
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

// noHardwareEncoder, set in the environment, keeps streams on OpenH264.
const noHardwareEncoder = "LINKCORD_NO_HW_ENCODER"

// hardwareFailed is set once the graphics card's encoder failed mid-stream:
// encoders opened after it are OpenH264's.
var hardwareFailed atomic.Bool

// encoder encodes pictures to H264: with the graphics card's encoder where
// there is one, which takes NV12, else with OpenH264, which takes I420. All
// its calls must come from one OS thread (runtime.LockOSThread), as the
// graphics card's encoder is a COM object of the thread it was made on.
type encoder struct {
	c    *C.lc_encoder
	hw   *C.lc_hwenc
	w, h int
	// name is the graphics card encoder's, or "OpenH264".
	name string
	idr  bool
}

func newEncoder(w, h, fps, bitrate, idr int) (*encoder, error) {
	if os.Getenv(noHardwareEncoder) == "" && !hardwareFailed.Load() {
		var name [128]uint16
		if hw := C.lc_hwenc_open(C.int(w), C.int(h), C.int(fps), C.int(bitrate), C.int(idr), (*C.uint16_t)(unsafe.Pointer(&name[0])), 128); hw != nil {
			return &encoder{hw: hw, w: w, h: h, name: windows.UTF16ToString(name[:])}, nil
		}
	}
	var cerr C.int
	c := C.lc_h264_open(C.int(w), C.int(h), C.int(fps), C.int(bitrate), C.int(idr), &cerr)
	if c == nil {
		return nil, fmt.Errorf("golive: could not start the H264 encoder (%d)", int(cerr))
	}
	return &encoder{c: c, w: w, h: h, name: "OpenH264"}, nil
}

// nv12 reports whether the encoder takes NV12 pictures rather than I420.
func (e *encoder) nv12() bool { return e.hw != nil }

// hardware reports whether the graphics card encodes.
func (e *encoder) hardware() bool { return e.hw != nil }

// encode encodes the picture, of the encoder's size and format, and returns
// a copy of an access unit, nil if there is none for now, its time and
// whether it is an IDR. The graphics card's encoder may return the
// picture of the call before, with that picture's time.
func (e *encoder) encode(pic []byte, tsMs int64) ([]byte, int64, bool, error) {
	var out *C.uint8_t
	var n, idr C.int
	p := (*C.uint8_t)(unsafe.Pointer(&pic[0]))
	if e.hw != nil {
		force := C.int(0)
		if e.idr {
			force, e.idr = 1, false
		}
		var ts C.int64_t
		if r := C.lc_hwenc_encode(e.hw, p, C.int64_t(tsMs), force, &out, &n, &ts, &idr); r != 0 {
			return nil, 0, false, fmt.Errorf("golive: the graphics card could not encode a picture (%s)", e.name)
		}
		if n == 0 {
			return nil, 0, false, nil
		}
		return C.GoBytes(unsafe.Pointer(out), n), int64(ts), idr != 0, nil
	}
	if r := C.lc_h264_encode(e.c, p, C.int64_t(tsMs), &out, &n, &idr); r != 0 {
		return nil, 0, false, fmt.Errorf("golive: H264 encoding failed (%d)", int(r))
	}
	if n == 0 {
		return nil, 0, false, nil
	}
	return C.GoBytes(unsafe.Pointer(out), n), tsMs, idr != 0, nil
}

func (e *encoder) forceIDR() {
	if e.hw != nil {
		e.idr = true
		return
	}
	C.lc_h264_force_idr(e.c)
}

func (e *encoder) setBitrate(bitrate int) {
	if e.hw != nil {
		C.lc_hwenc_set_bitrate(e.hw, C.int(bitrate))
		return
	}
	C.lc_h264_set_bitrate(e.c, C.int(bitrate))
}

func (e *encoder) close() {
	if e.hw != nil {
		C.lc_hwenc_close(e.hw)
		return
	}
	C.lc_h264_close(e.c)
}

// convertBands is how many bands a picture is split into to convert, each
// on its own thread: the cores there are, up to 8.
var convertBands = min(max(runtime.NumCPU(), 1), 8)

// toYUV scales the picture to w by h into dst, in NV12 if nv12 or else
// I420, converting bands of rows on several threads at once.
func (p picture) toYUV(dst []byte, w, h int, nv12 bool) {
	n := C.int(0)
	if nv12 {
		n = 1
	}
	src, d := (*C.uint8_t)(p.pix), (*C.uint8_t)(unsafe.Pointer(&dst[0]))
	// Bands of an even number of rows, at least 32 each, as a band shorter
	// than that is not worth a thread.
	bands := min(convertBands, max(h/32, 1))
	if bands == 1 {
		C.lc_bgra_to_yuv(src, C.int(p.w), C.int(p.h), C.int(p.stride), d, C.int(w), C.int(h), n, 0, C.int(h))
		return
	}
	step := (h/bands + 1) &^ 1
	var wg sync.WaitGroup
	for y0 := 0; y0 < h; y0 += step {
		y1 := min(y0+step, h)
		wg.Add(1)
		go func() {
			defer wg.Done()
			C.lc_bgra_to_yuv(src, C.int(p.w), C.int(p.h), C.int(p.stride), d, C.int(w), C.int(h), n, C.int(y0), C.int(y1))
		}()
	}
	wg.Wait()
}

// setSIMD turns the SSE2 conversion on or off, for tests.
func setSIMD(on bool) {
	if on {
		C.lc_simd = 1
	} else {
		C.lc_simd = 0
	}
}

// toYUVOnce is toYUV on this thread alone.
func (p picture) toYUVOnce(dst []byte, w, h int, nv12 bool) {
	n := C.int(0)
	if nv12 {
		n = 1
	}
	C.lc_bgra_to_yuv((*C.uint8_t)(p.pix), C.int(p.w), C.int(p.h), C.int(p.stride), (*C.uint8_t)(unsafe.Pointer(&dst[0])), C.int(w), C.int(h), n, 0, C.int(h))
}

// i420ToNV12 and nv12ToI420 move a picture of w by h from one layout of its
// colour to the other, for the rare picture converted before the encoder
// it goes to was known.
func i420ToNV12(dst, src []byte, w, h int) {
	ys, cs := w*h, (w/2)*(h/2)
	copy(dst[:ys], src[:ys])
	u, v, uv := src[ys:ys+cs], src[ys+cs:ys+2*cs], dst[ys:ys+2*cs]
	for i := range cs {
		uv[i*2], uv[i*2+1] = u[i], v[i]
	}
}

func nv12ToI420(dst, src []byte, w, h int) {
	ys, cs := w*h, (w/2)*(h/2)
	copy(dst[:ys], src[:ys])
	uv, u, v := src[ys:ys+2*cs], dst[ys:ys+cs], dst[ys+cs:ys+2*cs]
	for i := range cs {
		u[i], v[i] = uv[i*2], uv[i*2+1]
	}
}

// picture is what a capture grabbed: BGRA pixels in C memory, valid until
// the next grab.
type picture struct {
	pix          unsafe.Pointer
	w, h, stride int
	// changed is false when the picture is known to be the same as the
	// last one grabbed: nothing on the screen and the pointer moved.
	changed bool
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
	var w, h, stride, g, changed C.int
	pix := C.lc_capture_grab(c.c, &w, &h, &stride, &g, &changed)
	if pix == nil {
		return picture{}, false, g != 0
	}
	return picture{pix: unsafe.Pointer(pix), w: int(w), h: int(h), stride: int(stride), changed: changed != 0}, true, false
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
