package hotkey

import (
	"slices"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procMapVirtualKeyW   = user32.NewProc("MapVirtualKeyW")
	procGetKeyNameTextW  = user32.NewProc("GetKeyNameTextW")
)

// poll is how often the keys are looked at. It is short enough for a key
// to talk not to cut off the first word.
const poll = 15 * time.Millisecond

// held reports whether k is held now. GetAsyncKeyState tells whatever window
// has the keyboard, and needs neither a hook nor a window of its own, back
// to Windows 7.
func held(k Key) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(k))
	return r&0x8000 != 0
}

// layoutName is the name of the key in the keyboard layout, for keys whose
// letter depends on it, such as Ñ.
func layoutName(k Key) string {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(k), 0) // MAPVK_VK_TO_VSC
	if scan == 0 {
		return ""
	}
	var buf [64]uint16
	n, _, _ := procGetKeyNameTextW.Call(scan<<16, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

// Watcher watches the keys of its binds, and records combinations.
type Watcher struct {
	mu    sync.Mutex
	binds []Bind
	// down are the binds whose keys are all held.
	down []bool
	// record is given the next combination pressed, which then does nothing
	// else; recorded is what of it was pressed so far.
	record   func(Combo)
	recorded Combo
	stop     chan struct{}
}

// Start starts watching keys, for no binds yet.
func Start() *Watcher {
	w := &Watcher{stop: make(chan struct{})}
	go w.run()
	return w
}

// Set replaces the binds watched.
func (w *Watcher) Set(binds []Bind) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.release()
	w.binds = slices.Clone(binds)
	w.down = make([]bool, len(binds))
}

// Record gives done the next combination of keys pressed, once they are all
// let go, rather than act on it; nil stops recording. Like the functions of
// binds, done is called on the goroutine that watches the keys, and must
// not call the Watcher.
func (w *Watcher) Record(done func(Combo)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.release()
	w.record, w.recorded = done, nil
}

// Close stops watching.
func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.release()
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
}

// release lets go of the binds held, as if their keys were.
func (w *Watcher) release() {
	for i, down := range w.down {
		if down && w.binds[i].Up != nil {
			w.binds[i].Up()
		}
		w.down[i] = false
	}
}

func (w *Watcher) run() {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
		w.check()
	}
}

// check looks at the keys once.
func (w *Watcher) check() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.record != nil {
		w.checkRecord()
		return
	}
	for i, b := range w.binds {
		down := len(b.Keys) > 0
		for _, k := range b.Keys {
			if !held(k) {
				down = false
				break
			}
		}
		if down == w.down[i] {
			continue
		}
		w.down[i] = down
		if down && b.Down != nil {
			b.Down()
		} else if !down && b.Up != nil {
			b.Up()
		}
	}
}

// checkRecord adds the keys held to the combination recorded, and gives it
// once none is.
func (w *Watcher) checkRecord() {
	pressed := false
	for k := Key(1); k < 0xff; k++ {
		if !recordable(k) || !held(k) {
			continue
		}
		pressed = true
		if !slices.Contains(w.recorded, k) {
			w.recorded = append(w.recorded, k)
		}
	}
	if pressed || len(w.recorded) == 0 {
		return
	}
	done, combo := w.record, w.recorded.sorted()
	w.record, w.recorded = nil, nil
	done(combo)
}
