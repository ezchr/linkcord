//go:build windows && !no_voice

package golive

/*
#include "native.h"
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/ezchr/linkcord/internal/voice"
	"golang.org/x/sys/windows"
)

// startSound captures the sound of src into pcm, as 48 kHz stereo samples:
// that of the window's program for a window, and for a screen that of every
// program but Linkcord and Discord, as Discord leaves itself out, so that
// the voice channel is not sent back to it. Where Windows cannot tell
// programs apart, as Windows 7, it is all that the speakers play. It
// returns how it captures, and what stops it.
func startSound(src Source, pcm chan<- []int16) (how string, stop func(), err error) {
	if src.Kind == Window && src.Window != 0 {
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(windows.HWND(src.Window), &pid); err == nil && pid != 0 {
			if stop, err := captureProcess(pid, pcm); err == nil {
				return "the window's program", stop, nil
			}
		}
	} else if stop, err := mixPrograms(pcm); err == nil {
		return "every program but Linkcord and Discord", stop, nil
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

// onCaptureThread runs f on a thread of its own, which the captures it
// opens belong to, until done is closed. f reports through opened whether
// it could start.
func onCaptureThread(f func(done <-chan struct{}, opened chan<- error)) (stop func(), err error) {
	done := make(chan struct{})
	opened := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		f(done, opened)
	}()
	if err := <-opened; err != nil {
		return nil, err
	}
	return func() { close(done) }, nil
}

// openProcess starts capturing the sound of the process tree of pid.
func openProcess(pid uint32) (*C.lc_ploop, error) {
	var cerr C.int
	c := C.lc_ploop_open(C.uint32_t(pid), 1, &cerr)
	if c == nil {
		return nil, fmt.Errorf("process loopback unavailable (%#x)", uint32(cerr))
	}
	return c, nil
}

// captureProcess captures the sound of the process tree of pid into pcm.
func captureProcess(pid uint32, pcm chan<- []int16) (stop func(), err error) {
	return onCaptureThread(func(done <-chan struct{}, opened chan<- error) {
		c, err := openProcess(pid)
		opened <- err
		if err != nil {
			return
		}
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
	})
}

const (
	// mixDelay is how far behind the mix is, for every program captured to
	// have given its sound by then.
	mixDelay = 40 * time.Millisecond
	// maxQueued is how many samples of a program are kept at most, beyond
	// which it is cut back to mixDelay rather than fall behind.
	maxQueued = 48000 / 5 * 2
)

// mixPrograms captures the sound of every program playing but Linkcord and
// Discord, each on its own, and mixes them into pcm: Windows leaves one
// program out of a capture at most.
func mixPrograms(pcm chan<- []int16) (stop func(), err error) {
	return onCaptureThread(func(done <-chan struct{}, opened chan<- error) {
		// Windows before 10 2004 cannot capture a program on its own.
		probe, err := openProcess(uint32(os.Getpid()))
		opened <- err
		if err != nil {
			return
		}
		C.lc_ploop_close(probe)
		m := &mixer{pcm: pcm, self: uint32(os.Getpid()), progs: make(map[uint32]*program), failed: make(map[uint32]bool)}
		defer m.close()
		m.run(done)
	})
}

// mixer mixes the sound of the programs it captures.
type mixer struct {
	pcm   chan<- []int16
	self  uint32
	progs map[uint32]*program
	// failed are the programs that could not be captured, not tried again.
	failed map[uint32]bool
}

// program is the sound of a process tree, and what of it is not mixed yet.
type program struct {
	c   *C.lc_ploop
	pcm []int16
}

func (m *mixer) run(done <-chan struct{}) {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	buf := make([]int16, 9600*2)
	start := time.Now()
	var mixed int64
	var scanned time.Time
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		// Programs that start playing are found within a second.
		if time.Since(scanned) >= time.Second {
			scanned = time.Now()
			m.scan()
		}
		for pid, p := range m.progs {
			n := int(C.lc_ploop_read(p.c, (*C.int16_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)/2)))
			if n < 0 {
				C.lc_ploop_close(p.c)
				delete(m.progs, pid)
				continue
			}
			p.pcm = append(p.pcm, buf[:n*2]...)
			if len(p.pcm) > maxQueued {
				p.pcm = p.pcm[len(p.pcm)-int(mixDelay*48000/time.Second)*2:]
			}
		}
		due := int64((time.Since(start)-mixDelay)*48000/time.Second) - mixed
		if due <= 0 {
			continue
		}
		out := make([]int16, due*2)
		for _, p := range m.progs {
			take := min(len(p.pcm), len(out))
			for i, v := range p.pcm[:take] {
				out[i] = int16(max(math.MinInt16, min(math.MaxInt16, int32(out[i])+int32(v))))
			}
			p.pcm = p.pcm[take:]
		}
		mixed += due
		send(m.pcm, out)
	}
}

// scan starts capturing the programs that started playing.
func (m *mixer) scan() {
	var ids [64]C.uint32_t
	n := int(C.lc_audio_pids(&ids[0], C.int(len(ids))))
	procs := processes()
	for _, id := range ids[:n] {
		pid := uint32(id)
		if m.progs[pid] != nil || m.failed[pid] || !procs.shareable(pid, m.self) {
			continue
		}
		// A program in the tree of one captured is in its sound already.
		covered := false
		for q := range m.progs {
			if procs.descends(pid, q) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		c, err := openProcess(pid)
		if err != nil {
			m.failed[pid] = true
			continue
		}
		// One whose tree holds programs captured takes their place.
		for q, p := range m.progs {
			if procs.descends(q, pid) {
				C.lc_ploop_close(p.c)
				delete(m.progs, q)
			}
		}
		m.progs[pid] = &program{c: c}
		slog.Info("stream sound from", "program", procs[pid].exe, "pid", pid)
	}
}

func (m *mixer) close() {
	for _, p := range m.progs {
		C.lc_ploop_close(p.c)
	}
}

// proc is the parent and the executable, in lower case, of a process, and
// when it started, 0 if Windows does not tell.
type proc struct {
	parent  uint32
	exe     string
	started int64
}

// procTable is every process.
type procTable map[uint32]proc

func processes() procTable {
	t := make(procTable)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return t
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		t[e.ProcessID] = proc{e.ParentProcessID, strings.ToLower(windows.UTF16ToString(e.ExeFile[:])), processStart(e.ProcessID)}
	}
	return t
}

// processStart is when the process pid started, 0 if it cannot be told.
func processStart(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0
	}
	return created.Nanoseconds()
}

// descends reports whether pid is in the tree of root: root itself or a
// process it started, or one that one of those started. A parent that
// started after its child is not one: the parent ended, and Windows gave its
// ID to a new process, which would otherwise take the child's sound away.
func (t procTable) descends(pid, root uint32) bool {
	for range 64 {
		if pid == root {
			return true
		}
		p, ok := t[pid]
		if !ok || p.parent == 0 || p.parent == pid {
			return false
		}
		if parent, ok := t[p.parent]; ok && parent.started != 0 && p.started != 0 && parent.started > p.started {
			return false
		}
		pid = p.parent
	}
	return false
}

// shareable reports whether the sound of the tree of pid may be streamed:
// it holds neither Linkcord, self, nor Discord, whose sound is the voice
// channel's.
func (t procTable) shareable(pid, self uint32) bool {
	if _, ok := t[pid]; !ok {
		return false
	}
	for q, p := range t {
		if q == self || strings.HasPrefix(p.exe, "discord") {
			if t.descends(pid, q) || t.descends(q, pid) {
				return false
			}
		}
	}
	return true
}

// send gives pcm the samples, dropped if it is full rather than block the
// capture.
func send(pcm chan<- []int16, s []int16) {
	select {
	case pcm <- s:
	default:
	}
}
