package gui

import (
	"syscall"
	"unsafe"

	"gioui.org/app"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procFlashWindow = user32.NewProc("FlashWindowEx")
	procMessageBeep = user32.NewProc("MessageBeep")
)

type flashInfo struct {
	size    uint32
	hwnd    uintptr
	flags   uint32
	count   uint32
	timeout uint32
}

const (
	flashTray        = 0x2
	flashTimerNoFG   = 0xc
	beepNotification = 0x40 // MB_ICONASTERISK
)

// notify flashes the taskbar button until the window is focused and plays
// the notification sound, which works the same on Windows 7 and later.
func (u *ui) notify() {
	if u.hwnd != 0 && !u.focused {
		fi := flashInfo{hwnd: u.hwnd, flags: flashTray | flashTimerNoFG}
		fi.size = uint32(unsafe.Sizeof(fi))
		procFlashWindow.Call(uintptr(unsafe.Pointer(&fi)))
	}
	procMessageBeep.Call(beepNotification)
}

// viewEvent keeps the native window handle, which notify needs.
func (u *ui) viewEvent(e app.ViewEvent) {
	if v, ok := e.(app.Win32ViewEvent); ok {
		u.hwnd = v.HWND
	}
}
