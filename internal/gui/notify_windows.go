package gui

import (
	"os"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"github.com/ezchr/linkcord/internal/dcsound"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procFlashWindow = user32.NewProc("FlashWindowEx")
)

type flashInfo struct {
	size    uint32
	hwnd    uintptr
	flags   uint32
	count   uint32
	timeout uint32
}

const (
	flashTray      = 0x2
	flashTimerNoFG = 0xc
)

// notify flashes the taskbar button until the window is focused and plays
// Discord's sound of a message, unless it is turned off in the settings.
func (u *ui) notify() {
	if u.hwnd != 0 && !u.focused {
		fi := flashInfo{hwnd: u.hwnd, flags: flashTray | flashTimerNoFG}
		fi.size = uint32(unsafe.Sizeof(fi))
		procFlashWindow.Call(uintptr(unsafe.Pointer(&fi)))
	}
	// Turned off in the settings, it is silent, not replaced by Windows'
	// beep.
	u.playSound(dcsound.Message)
}

// viewEvent keeps the native window handle, which notify needs. Started by
// Windows to start minimized, the window goes to the notification area as
// soon as it is there.
func (u *ui) viewEvent(e app.ViewEvent) {
	if v, ok := e.(app.Win32ViewEvent); ok {
		first := u.hwnd == 0 && v.HWND != 0
		u.hwnd = v.HWND
		if first && os.Getenv("LINKCORD_MINIMIZED") == "1" {
			os.Unsetenv("LINKCORD_MINIMIZED")
			// Shown that way, it says nothing of still running.
			u.trayHinted = true
			u.hideToTray()
		}
	}
}
