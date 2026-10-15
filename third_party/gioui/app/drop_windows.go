// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"syscall"
	"unsafe"
)

// FilesDropped, if set, is called with the paths of the files dropped on a
// window from Explorer or another program, on the window's thread.
var FilesDropped func(paths []string)

var (
	dropShell32         = syscall.NewLazyDLL("shell32.dll")
	procDragAcceptFiles = dropShell32.NewProc("DragAcceptFiles")
	procDragQueryFileW  = dropShell32.NewProc("DragQueryFileW")
	procDragFinish      = dropShell32.NewProc("DragFinish")
)

const wmDropFiles = 0x0233

// acceptFiles has the window take files dropped on it.
func acceptFiles(hwnd uintptr) {
	procDragAcceptFiles.Call(uintptr(hwnd), 1)
}

// filesDropped hands the files of a WM_DROPFILES to FilesDropped.
func filesDropped(drop uintptr) {
	defer procDragFinish.Call(drop)
	n, _, _ := procDragQueryFileW.Call(drop, 0xFFFFFFFF, 0, 0)
	var paths []string
	for i := uintptr(0); i < n; i++ {
		size, _, _ := procDragQueryFileW.Call(drop, i, 0, 0)
		if size == 0 {
			continue
		}
		buf := make([]uint16, size+1)
		procDragQueryFileW.Call(drop, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	if f := FilesDropped; f != nil && len(paths) > 0 {
		f(paths)
	}
}
