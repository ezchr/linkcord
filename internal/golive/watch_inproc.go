//go:build windows && !no_voice

package golive

/*
#include "native.h"
*/
import "C"

import (
	"errors"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// forceFFmpegDecoder, set in the environment, has streams decoded by FFmpeg
// as before rather than inside Linkcord.
const forceFFmpegDecoder = "LINKCORD_FFMPEG_DECODER"

// inprocUnavailable is set once the in-process decoder could not open: the
// system cannot, and later streams go straight to FFmpeg.
var inprocUnavailable atomic.Bool

// InProcessDecoding reports whether streams can be watched without FFmpeg,
// decoded by the graphics card inside Linkcord. It opens a decoder once to
// find out.
func InProcessDecoding() bool {
	if os.Getenv(forceFFmpegDecoder) != "" || os.Getenv("LINKCORD_NO_HW_DECODER") != "" || inprocUnavailable.Load() {
		return false
	}
	inprocProbe.Do(func() {
		d := C.lc_hwdec_open(1280, 720)
		if d == nil {
			inprocUnavailable.Store(true)
			return
		}
		C.lc_hwdec_close(d)
	})
	return !inprocUnavailable.Load()
}

var inprocProbe sync.Once

// hwDecoder is Media Foundation's H264 decoder on the graphics card, which
// gives each picture already scaled to the size asked and in RGBA: see
// mfdec_windows.c. Its calls must not overlap.
type hwDecoder struct {
	c *C.lc_hwdec
}

func openHWDecoder(w, h int) (*hwDecoder, error) {
	c := C.lc_hwdec_open(C.int(w), C.int(h))
	if c == nil {
		return nil, errors.New("golive: the graphics card cannot decode the stream")
	}
	return &hwDecoder{c: c}, nil
}

// decode decodes the access unit au into dst, which it reports whether it
// wrote a picture to.
func (d *hwDecoder) decode(au []byte, dst *image.RGBA) (bool, error) {
	if len(au) == 0 {
		return false, nil
	}
	var got C.int
	r := C.lc_hwdec_decode(d.c, (*C.uint8_t)(unsafe.Pointer(&au[0])), C.int(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&dst.Pix[0])), C.int(dst.Rect.Dx()), C.int(dst.Rect.Dy()), &got)
	if r != 0 {
		return false, errors.New("golive: the graphics card's decoder failed")
	}
	return got != 0, nil
}

func (d *hwDecoder) close() {
	if d.c != nil {
		C.lc_hwdec_close(d.c)
		d.c = nil
	}
}

// inprocDecoder decodes the watched stream inside Linkcord: the decoder
// input of a Watch, as FFmpeg's pipe is, that puts each picture in the ring
// as FFmpeg's reader does. Pictures come at the size the view shows them,
// which follows the view without starting over.
type inprocDecoder struct {
	w  *Watch
	mu sync.Mutex
	d  *hwDecoder
	// A few pictures are used in turn, as with FFmpeg.
	ring   [4]*image.RGBA
	i      int
	closed bool
	// tex is the shared texture pictures go into, when the GUI draws them
	// from the graphics card; current, read under w.mu, is the one that
	// has a picture, which the GUI draws, nil when it takes w.frame.
	tex      *SharedFrame
	current  *SharedFrame
	noShared bool
}

// startInproc starts decoding a sw×sh stream inside Linkcord, in place of
// any decoder running.
func (w *Watch) startInproc(sw, sh int) error {
	hd, err := openHWDecoder(sw, sh)
	if err != nil {
		inprocUnavailable.Store(true)
		return err
	}
	d := &inprocDecoder{w: w, d: hd}
	ow, oh := w.decodeSize(sw, sh)
	w.mu.Lock()
	select {
	case <-w.done:
		w.mu.Unlock()
		hd.close()
		return errors.New("golive: stream ended before decoder startup")
	default:
	}
	old, oldIn := w.decoder, w.decIn
	w.decoder, w.decIn = nil, d
	w.decW, w.decH = ow, oh
	w.mu.Unlock()
	if old != nil && old.Process != nil {
		old.Process.Kill()
	}
	if oldIn != nil {
		oldIn.Close()
	}
	return nil
}

// Write decodes one picture. A failure of the decoder has the stream
// decoded by FFmpeg from the next keyframe.
func (d *inprocDecoder) Write(pic []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return len(pic), nil
	}
	w := d.w
	w.mu.Lock()
	// Never the picture shown nor the one the GUI holds: with four, one of
	// the next two is always free.
	i := d.i
	for d.ring[i] != nil && (d.ring[i] == w.frame || d.ring[i] == w.taken) {
		i = (i + 1) % len(d.ring)
	}
	sw, sh := w.srcW, w.srcH
	w.mu.Unlock()
	// Decoded straight at the size shown: the graphics card averages it
	// down, rather than shrink here.
	ow, oh := w.viewSize(w.decodeSize(sw, sh))
	if done, err := d.writeShared(pic, ow, oh); done || err != nil {
		if err != nil {
			d.fail(err)
			return 0, err
		}
		return len(pic), nil
	}
	if d.ring[i] == nil || d.ring[i].Rect.Dx() != ow || d.ring[i].Rect.Dy() != oh {
		d.ring[i] = image.NewRGBA(image.Rect(0, 0, ow, oh))
	}
	img := d.ring[i]
	got, err := d.d.decode(pic, img)
	if err != nil {
		d.fail(err)
		return 0, err
	}
	if !got {
		return len(pic), nil
	}
	d.i = (i + 1) % len(d.ring)
	w.mu.Lock()
	current := w.decIn == d
	d.current = nil
	if current {
		w.frame = img
		w.decW, w.decH = ow, oh
		if w.waiting.IsZero() {
			w.waiting = time.Now()
		}
	}
	w.mu.Unlock()
	if current {
		w.frames.Add(1)
		if w.p.OnFrame != nil {
			w.p.OnFrame()
		}
	}
	return len(pic), nil
}

// fail gives up the decoder: the watch goes on with FFmpeg, which starts
// at the next keyframe. Called with d.mu held.
func (d *inprocDecoder) fail(err error) {
	w := d.w
	d.closed = true
	d.dropShared()
	d.d.close()
	if !w.inprocOff.Swap(true) {
		w.log.Warn("the in-process decoder failed; decoding with FFmpeg", "err", err)
	}
	w.mu.Lock()
	if w.decIn == d {
		w.decIn = nil
	}
	w.mu.Unlock()
	w.requireKeyframe()
}

func (d *inprocDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		d.dropShared()
		d.d.close()
	}
	return nil
}

// readbackForced, set in the environment, has every picture read back
// from the graphics card and drawn from memory, as before shared textures.
const readbackForced = "LINKCORD_STREAM_READBACK"

// sharedUnavailable is set once the GUI could not draw a shared texture,
// as with a renderer other than Direct3D 11's: pictures are read back from
// then on.
var sharedUnavailable atomic.Bool

// sharedFrames reports whether pictures go to the GUI as shared textures.
func sharedFrames() bool {
	return !sharedUnavailable.Load() && os.Getenv(readbackForced) == ""
}

// SharedFrame is a picture kept on the graphics card, in a texture of the
// decoder's Direct3D 11 device that the GUI's device opens and draws: no
// copy goes through memory. It is a paint.SharedImage of Linkcord's Gio.
type SharedFrame struct {
	mu      sync.Mutex
	t       *C.lc_hwtex
	size    image.Point
	version atomic.Uint64
	log     func(err error)
}

// Size is the size of the picture.
func (f *SharedFrame) Size() image.Point { return f.size }

// UseSharedTexture calls fn with the texture's shared handle and version,
// the texture kept alive meanwhile; with 0 once it is freed.
func (f *SharedFrame) UseSharedTexture(fn func(handle uintptr, version uint64)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.version.Load()
	if f.t == nil || v == 0 {
		fn(0, 0)
		return
	}
	fn(uintptr(C.lc_hwtex_handle(f.t)), v)
}

// SharedTextureFailed is called by the GUI's renderer when it cannot draw
// the texture: pictures are read back from then on.
func (f *SharedFrame) SharedTextureFailed(err error) {
	if !sharedUnavailable.Swap(true) && f.log != nil {
		f.log(err)
	}
}

func (f *SharedFrame) free() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.t != nil {
		C.lc_hwtex_free(f.t)
		f.t = nil
	}
}

// newSharedFrame makes a w by h shared texture on the decoder's device, or
// returns nil.
func (d *hwDecoder) newSharedFrame(w, h int) *SharedFrame {
	t := C.lc_hwtex_new(d.c, C.int(w), C.int(h))
	if t == nil {
		return nil
	}
	return &SharedFrame{t: t, size: image.Pt(w, h)}
}

// decodeShared decodes the access unit au into f, which it reports whether
// it wrote a picture to, raising its version.
func (d *hwDecoder) decodeShared(au []byte, f *SharedFrame) (bool, error) {
	if len(au) == 0 {
		return false, nil
	}
	var got C.int
	if C.lc_hwdec_decode_tex(d.c, (*C.uint8_t)(unsafe.Pointer(&au[0])), C.int(len(au)), f.t, &got) != 0 {
		return false, errors.New("golive: the graphics card's decoder failed")
	}
	if got != 0 {
		f.version.Add(1)
	}
	return got != 0, nil
}

// writeShared decodes pic into the shared texture, at ow by oh, reporting
// whether it did; false without an error has the picture read back
// instead. Called with d.mu held.
func (d *inprocDecoder) writeShared(pic []byte, ow, oh int) (bool, error) {
	if d.noShared || !sharedFrames() || len(pic) == 0 {
		if d.tex != nil {
			d.dropShared()
		}
		return false, nil
	}
	w := d.w
	if d.tex == nil || d.tex.size != image.Pt(ow, oh) {
		f := d.d.newSharedFrame(ow, oh)
		if f == nil {
			d.noShared = true
			w.log.Info("no shared texture for the stream; reading pictures back")
			return false, nil
		}
		// The last one stays until this one has a picture: the GUI
		// keeps drawing its last copy, and once freed sees no handle.
		if d.tex != nil && d.tex != d.current {
			d.tex.free()
		}
		f.log = func(err error) {
			w.log.Warn("the GUI cannot draw the stream from the graphics card; reading pictures back", "err", err)
		}
		d.tex = f
	}
	got, err := d.d.decodeShared(pic, d.tex)
	if err != nil {
		return false, err
	}
	if C.lc_hwdec_no_shared(d.d.c) != 0 {
		d.noShared = true
		d.dropShared()
		w.log.Info("the graphics card gives no RGBA for a shared texture; reading pictures back")
		// This picture is lost; the next is read back.
		return true, nil
	}
	if !got {
		return true, nil
	}
	w.mu.Lock()
	current := w.decIn == d
	var old *SharedFrame
	if current {
		if d.current != d.tex {
			old = d.current
		}
		d.current = d.tex
		w.frame = nil
		w.decW, w.decH = ow, oh
		if w.waiting.IsZero() {
			w.waiting = time.Now()
		}
	}
	w.mu.Unlock()
	if old != nil {
		old.free()
	}
	if current {
		w.frames.Add(1)
		if w.p.OnFrame != nil {
			w.p.OnFrame()
		}
	}
	return true, nil
}

// dropShared frees the shared textures, the GUI going back to w.frame.
// Called with d.mu held.
func (d *inprocDecoder) dropShared() {
	w := d.w
	w.mu.Lock()
	cur := d.current
	d.current = nil
	w.mu.Unlock()
	if cur != nil && cur != d.tex {
		cur.free()
	}
	if d.tex != nil {
		d.tex.free()
		d.tex = nil
	}
}

// SharedFrame is the latest picture as a texture on the graphics card, or
// nil when pictures come through Frame instead.
func (w *Watch) SharedFrame() *SharedFrame {
	w.mu.Lock()
	defer w.mu.Unlock()
	d, ok := w.decIn.(*inprocDecoder)
	if !ok || d.current == nil {
		return nil
	}
	w.waiting = time.Time{}
	return d.current
}
