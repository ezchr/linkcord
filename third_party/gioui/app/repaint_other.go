// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package app

// Repaint invalidates the window; see the Windows version.
func (w *Window) Repaint() string {
	w.invMu.Lock()
	w.mayInvalidate = w.driver != nil
	w.invMu.Unlock()
	w.Invalidate()
	return ""
}
