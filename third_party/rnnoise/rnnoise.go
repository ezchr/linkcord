// Package rnnoise removes noise from speech with Xiph's RNNoise 0.1.1
// (https://github.com/xiph/rnnoise, BSD-3-Clause, see COPYING), whose
// sources and trained model are vendored here unchanged, beside an empty
// config.h. It works on 48 kHz mono in frames of 10 ms.
package rnnoise

/*
#cgo CFLAGS: -O2 -I${SRCDIR}
#include <stdlib.h>
#include "rnnoise.h"
*/
import "C"

import "unsafe"

// FrameSize is how many samples Process takes: 10 ms at 48 kHz.
const FrameSize = 480

// State removes the noise of one stream of sound, which it follows from
// frame to frame.
type State struct {
	st  *C.DenoiseState
	buf *[FrameSize]C.float
}

// New returns a State with RNNoise's own model.
func New() *State {
	return &State{
		st:  C.rnnoise_create(nil),
		buf: (*[FrameSize]C.float)(C.malloc(C.size_t(FrameSize * C.sizeof_float))),
	}
}

// Process removes the noise of frame, FrameSize samples as loud as 16-bit
// ones, in place, and returns how likely it holds speech, from 0 to 1.
func (s *State) Process(frame []float32) float32 {
	for i, v := range frame[:FrameSize] {
		s.buf[i] = C.float(v)
	}
	vad := C.rnnoise_process_frame(s.st, &s.buf[0], &s.buf[0])
	for i := range FrameSize {
		frame[i] = float32(s.buf[i])
	}
	return float32(vad)
}

// Close frees the State.
func (s *State) Close() {
	C.rnnoise_destroy(s.st)
	C.free(unsafe.Pointer(s.buf))
}
