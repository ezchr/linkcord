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
		d.d.close()
	}
	return nil
}
