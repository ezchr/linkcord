// Package aec3 removes from what a microphone captures the echo of what the
// speakers play, with AEC3, the echo canceller of WebRTC's audio processing
// module, which Discord's and Chromium's calls use. The sources are those
// of webrtc-audio-processing 2.1
// (https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing,
// BSD-3-Clause, see webrtc/LICENSE and webrtc/PATENTS), unchanged, in
// webrtc, with the headers of Abseil 20240722.0 they include (Apache-2.0,
// see abseil/LICENSE) in abseil. Only what AEC3 needs is here. The
// webrtc_*.cc files of this folder build them, aec3_c.cc is the C
// interface and aec3_noavx2.cc stands in for the AVX2 files, not built.
// It works on 48 kHz sound in frames of 10 ms.
package aec3

/*
#cgo CPPFLAGS: -I${SRCDIR} -I${SRCDIR}/webrtc -I${SRCDIR}/abseil -DWEBRTC_WIN -DNOMINMAX -D_WINSOCKAPI_ -DNDEBUG -DWEBRTC_APM_DEBUG_DUMP=0 -DRTC_DISABLE_LOGGING
#cgo !windows CPPFLAGS: -UWEBRTC_WIN -DWEBRTC_POSIX
#cgo CFLAGS: -O2 -w
#cgo CXXFLAGS: -std=c++17 -O2 -w
#cgo windows LDFLAGS: -static
#include "aec3_c.h"
*/
import "C"

// FrameSize is how many samples of a channel Render and Capture take:
// 10 ms at 48 kHz.
const FrameSize = 480

// Canceller removes the echo of one microphone, following the room and the
// delay of the devices from frame to frame. Render may be called while
// Capture runs; each is called from one goroutine at a time.
type Canceller struct {
	st       *C.lc_aec3
	channels int
}

// New returns a Canceller for a microphone captured with channels
// channels, 1 or 2. What is played is always mono.
func New(channels int) *Canceller {
	return &Canceller{st: C.lc_aec3_new(C.int(channels)), channels: channels}
}

// Render takes the next FrameSize samples that the speakers play, mono.
func (c *Canceller) Render(frame []int16) {
	C.lc_aec3_render(c.st, (*C.int16_t)(&frame[:FrameSize][0]))
}

// Capture removes the echo from the next frame the microphone captured,
// FrameSize samples of each channel, interleaved, in place.
func (c *Canceller) Capture(frame []int16) {
	C.lc_aec3_capture(c.st, (*C.int16_t)(&frame[:FrameSize*c.channels][0]))
}

// Metrics are AEC3's own estimates: how much quieter the echo arrives than
// it was played (erl) and how much of it is removed (erle), in dB, and the
// delay found between what is played and its echo.
func (c *Canceller) Metrics() (erl, erle float64, delayMs int) {
	var l, e C.double
	var d C.int
	C.lc_aec3_metrics(c.st, &l, &e, &d)
	return float64(l), float64(e), int(d)
}

// Close frees the Canceller.
func (c *Canceller) Close() {
	C.lc_aec3_free(c.st)
	c.st = nil
}
