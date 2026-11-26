package gui

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetLastInputInfo = windows.NewLazySystemDLL("user32.dll").NewProc("GetLastInputInfo")
	procGetTickCount     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
)

// systemIdle is how long since the user last used the keyboard or mouse,
// anywhere on the system.
func systemIdle() time.Duration {
	info := struct{ size, time uint32 }{size: 8}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0
	}
	now, _, _ := procGetTickCount.Call()
	// Both wrap at 49.7 days; the subtraction wraps with them.
	return time.Duration(uint32(now)-info.time) * time.Millisecond
}
