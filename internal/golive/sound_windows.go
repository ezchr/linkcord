//go:build windows && !no_voice

package golive

/*
#include "native.h"
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/sys/windows"
)

// startSound captures the sound of src into pcm, as 48 kHz stereo samples:
// that of the window's program for a window, and that of all but Linkcord
// for a screen, as Discord does, so that the voice channel is not sent back
// to it. Where Windows cannot tell programs apart, as Windows 7, it is all
// that the speakers play. It returns how it captures, and what stops it.
func startSound(src Source, pcm chan<- []int16) (how string, stop func(), err error) {
	pid, include := uint32(os.Getpid()), false
	if src.Kind == Window && src.Window != 0 {
		var p uint32
		if _, err := windows.GetWindowThreadProcessId(windows.HWND(src.Window), &p); err == nil && p != 0 {
			pid, include = p, true
		}
	}

	done := make(chan struct{})
	opened := make(chan error, 1)
	go func() {
		// The capture belongs to the thread it was opened on.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		inc := C.int(0)
		if include {
			inc = 1
		}
		var cerr C.int
		c := C.lc_ploop_open(C.uint32_t(pid), inc, &cerr)
		if c == nil {
			opened <- fmt.Errorf("process loopback unavailable (%#x)", uint32(cerr))
			return
		}
		opened <- nil
		defer C.lc_ploop_close(c)
		buf := make([]int16, 9600*2)
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			n := int(C.lc_ploop_read(c, (*C.int16_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)/2)))
			if n < 0 {
				return
			}
			if n > 0 {
				send(pcm, append([]int16(nil), buf[:n*2]...))
			}
		}
	}()
	if err := <-opened; err == nil {
		mode := "everything but Linkcord"
		if include {
			mode = "the window's program"
		}
		return mode, func() { close(done) }, nil
	} else if errors.Is(err, errors.ErrUnsupported) {
		return "", nil, err
	}

	stopLoopback, err := voice.StartLoopback(func(b []byte) {
		s := make([]int16, len(b)/2)
		for i := range s {
			s[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		}
		send(pcm, s)
	})
	if err != nil {
		return "", nil, err
	}
	return "all that plays", stopLoopback, nil
}

// send gives pcm the samples, dropped if it is full rather than block the
// capture.
func send(pcm chan<- []int16, s []int16) {
	select {
	case pcm <- s:
	default:
	}
}
