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
	// dev is the device whose textures the encoder takes, if any, held.
	dev unsafe.Pointer
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

// newEncoderOn opens the encoder of the graphics card of dev (an
// ID3D11Device, with a reference the encoder takes over), taking that
// device's textures; nil where it has none that does.
func newEncoderOn(dev unsafe.Pointer, w, h, fps, bitrate, idr int) *encoder {
	if os.Getenv(noHardwareEncoder) != "" || hardwareFailed.Load() {
		C.lc_com_release(dev)
		return nil
	}
	var name [128]uint16
	hw := C.lc_hwenc_open_on(dev, C.int(w), C.int(h), C.int(fps), C.int(bitrate), C.int(idr), (*C.uint16_t)(unsafe.Pointer(&name[0])), 128)
	if hw == nil {
		C.lc_com_release(dev)
		return nil
	}
	return &encoder{hw: hw, dev: dev, w: w, h: h, name: windows.UTF16ToString(name[:]) + ", converted by the graphics card"}
}

// textures reports whether the encoder takes textures of dev.
func (e *encoder) textures(dev unsafe.Pointer) bool { return e.dev != nil && e.dev == dev }

// encodeTexture is encode for an NV12 texture of the encoder's device.
func (e *encoder) encodeTexture(tex unsafe.Pointer, tsMs int64) ([]byte, int64, bool, error) {
	var out *C.uint8_t
	var n, idr C.int
	var ts C.int64_t
	force := C.int(0)
	if e.idr {
		force, e.idr = 1, false
	}
	if r := C.lc_hwenc_encode_texture(e.hw, tex, C.int64_t(tsMs), force, &out, &n, &ts, &idr); r != 0 {
		return nil, 0, false, fmt.Errorf("golive: the graphics card could not encode a picture (%s)", e.name)
	}
	if n == 0 {
		return nil, 0, false, nil
	}
	return C.GoBytes(unsafe.Pointer(out), n), int64(ts), idr != 0, nil
}

// gpuCapture captures a screen, or an uncovered window, and converts it to
// NV12 on the graphics card, from Windows 8 (see lc_gpucap in native.h).
type gpuCapture struct {
	g *C.lc_gpucap
}

// gpu statuses of a grab.
const (
	gpuPicture = 1
	gpuNone    = 0
	gpuThisCPU = -2 // capture this picture with the processor
	gpuNever   = -1 // the graphics card cannot capture: the processor does from now on
)

func newGPUCapture(src Source, cursor bool) *gpuCapture {
	if os.Getenv(noHardwareEncoder) != "" || hardwareFailed.Load() {
		return nil
	}
	cur := C.int(0)
	if cursor {
		cur = 1
	}
	g := C.lc_gpucap_open(C.uintptr_t(src.Window), C.int(src.X), C.int(src.Y), cur)
	if g == nil {
		return nil
	}
	return &gpuCapture{g: g}
}

// grab takes the latest picture: its size, a status, whether it differs
// from the last, and whether the window shared was closed.
func (g *gpuCapture) grab() (w, h, status int, changed, gone bool) {
	var cw, ch, cc, cg C.int
	r := C.lc_gpucap_grab(g.g, &cw, &ch, &cc, &cg)
	return int(cw), int(ch), int(r), cc != 0, cg != 0
}

// convert scales the last picture to ow by oh NV12 and returns the texture
// and its device, each with a reference of its own for the encoder.
func (g *gpuCapture) convert(ow, oh int) (tex, dev unsafe.Pointer, err error) {
	var slot C.int
	if C.lc_gpucap_convert(g.g, C.int(ow), C.int(oh), &slot) != 0 {
		return nil, nil, errors.New("golive: the graphics card could not convert the picture")
	}
	tex = C.lc_gpucap_texture(g.g, slot)
	dev = C.lc_gpucap_device(g.g)
	if tex == nil || dev == nil {
		releaseCOM(tex)
		releaseCOM(dev)
		return nil, nil, errors.New("golive: the graphics card's picture is gone")
	}
	return tex, dev, nil
}

// preview scales the last picture to w by h.
func (g *gpuCapture) preview(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if !g.previewInto(img) {
		return nil
	}
	return img
}

// previewInto makes the preview in img, at its size, false where it could
// not.
func (g *gpuCapture) previewInto(img *image.RGBA) bool {
	return C.lc_gpucap_preview(g.g, (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(img.Rect.Dx()), C.int(img.Rect.Dy())) == 0
}

func (g *gpuCapture) close() { C.lc_gpucap_close(g.g) }

// readNV12 reads a w by h NV12 texture back, for tests.
func readNV12(tex unsafe.Pointer, w, h int) []byte {
	b := make([]byte, w*h*3/2)
	if C.lc_texture_read_nv12(tex, (*C.uint8_t)(unsafe.Pointer(&b[0])), C.int(w), C.int(h)) != 0 {
		return nil
	}
	return b
}

// addRefCOM takes another reference to a COM object and returns it.
func addRefCOM(p unsafe.Pointer) unsafe.Pointer {
	if p != nil {
		C.lc_com_addref(p)
	}
	return p
}

// releaseCOM lets go of a reference to a COM object, if any.
func releaseCOM(p unsafe.Pointer) {
	if p != nil {
		C.lc_com_release(p)
	}
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
		releaseCOM(e.dev)
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
	p.intoRGBA(img)
	return img
}

// intoRGBA scales the picture to img's size, into img.
func (p picture) intoRGBA(img *image.RGBA) {
	C.lc_bgra_to_rgba((*C.uint8_t)(p.pix), C.int(p.w), C.int(p.h), C.int(p.stride), (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(img.Rect.Dx()), C.int(img.Rect.Dy()))
}

// capturer captures a screen or a window.
type capturer struct {
	c *C.lc_capture
	// src is what it captures.
	src Source
}

func newCapturer(src Source, cursor bool) *capturer {
	cur := C.int(0)
	if cursor {
		cur = 1
	}
	return &capturer{src: src, c: C.lc_capture_open(C.uintptr_t(src.Window), C.int(src.X), C.int(src.Y), C.int(src.W), C.int(src.H), cur)}
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

// release lets go of the screen's duplication, for the graphics card's
// capture, which cannot duplicate it while this holds it.
func (c *capturer) release() { C.lc_capture_release(c.c) }

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
