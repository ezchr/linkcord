// Package instance keeps Linkcord to one window per Windows session.
package instance

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowClass is the window class of Linkcord, which a second start looks for
// to bring the running one to the front. It must be set as the Gio app ID, so
// that renaming the executable does not change it.
const WindowClass = "Linkcord"

// mutexName is the session-wide lock that the running Linkcord holds. Local\
// keeps it to the Windows session, so other users signed in to the same PC
// can run their own Linkcord. Tests change it, and the window class looked
// for, so that they do not meet a Linkcord that is running.
var (
	mutexName  = `Local\Linkcord.SingleInstance`
	focusClass = WindowClass
	focusTries = 30
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

const (
	swShow    = 5
	swRestore = 9
)

// lock is the mutex of the running instance, held open until the process
// exits, when Windows releases it.
var lock windows.Handle

// Acquire reports whether this is the only Linkcord running in the Windows
// session. When another one runs, it brings that one's window to the front
// and returns false, and the caller should exit.
func Acquire() bool {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return true
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		focusRunning()
		return false
	}
	if err != nil {
		// Without the lock there is no way to tell; starting is better than
		// refusing to.
		return true
	}
	lock = h
	return true
}

// focusRunning brings the window of the running Linkcord to the front,
// restoring it if minimized. A Linkcord that is still starting has no window
// yet, so it is looked for during a few seconds.
func focusRunning() {
	class, err := windows.UTF16PtrFromString(focusClass)
	if err != nil {
		return
	}
	for range focusTries {
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
		if hwnd != 0 {
			if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
				procShowWindow.Call(hwnd, swRestore)
			} else {
				procShowWindow.Call(hwnd, swShow)
			}
			// This process was started by the user, so Windows lets it give
			// the focus away.
			procSetForegroundWindow.Call(hwnd)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
