package gui

import (
	"syscall"
	"unsafe"
)

var (
	procPostMessageW     = user32.NewProc("PostMessageW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetModuleHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	wmSetIcon      = 0x80
	iconSmall      = 0
	iconBig        = 1
	imageIcon      = 1
	lrLoadFromFile = 0x10
	smCxIcon       = 11
	smCxSmIcon     = 49
)

// setWindowIcon gives the window the icon in the .ico at path, small for its
// title and large for the taskbar, or the executable's own if path is "".
func setWindowIcon(hwnd uintptr, path string) {
	if hwnd == 0 {
		return
	}
	for _, k := range [...]struct{ which, metric uintptr }{{iconSmall, smCxSmIcon}, {iconBig, smCxIcon}} {
		size, _, _ := procGetSystemMetrics.Call(k.metric)
		var h uintptr
		if path != "" {
			if p, err := syscall.UTF16PtrFromString(path); err == nil {
				h, _, _ = procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, size, size, lrLoadFromFile)
			}
		}
		if h == 0 {
			instance, _, _ := procGetModuleHandleW.Call(0)
			h, _, _ = procLoadImageW.Call(instance, 1, imageIcon, size, size, 0)
		}
		if h != 0 {
			// Posted, not sent: the window's thread waits for the frame
			// being drawn, and sending from it would wait for that thread
			// in turn, which froze the window.
			procPostMessageW.Call(hwnd, wmSetIcon, k.which, h)
		}
	}
}
