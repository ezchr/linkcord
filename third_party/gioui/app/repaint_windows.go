// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"fmt"
	"syscall"
)

var procInvalidateRect = syscall.NewLazyDLL("user32.dll").NewProc("InvalidateRect")

// Repaint asks Windows to paint the window, which draws a frame through
// WM_PAINT even when invalidations are not being honoured, and clears a
// pending invalidation so Invalidate works again. It is safe for
// concurrent use and returns the window's redraw state, for logs.
func (w *Window) Repaint() string {
	w.invMu.Lock()
	d := w.driver
	may := w.mayInvalidate
	w.mayInvalidate = d != nil
	w.invMu.Unlock()
	win, ok := d.(*window)
	if !ok {
		return "no window"
	}
	state := fmt.Sprintf("may_invalidate=%v animating=%v size=%v minimized=%v queued_invalidate=%d",
		may, win.animating, win.config.Size, win.config.Mode == Minimized, len(win.loop.invalidates))
	procInvalidateRect.Call(uintptr(win.hwnd), 0, 0)
	win.Invalidate()
	return state
}
