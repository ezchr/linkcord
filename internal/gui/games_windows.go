package gui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// runningExecutables are the paths of the programs running, those whose
// path can be read.
func runningExecutables() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []string
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 {
			continue
		}
		if p := processPath(e.ProcessID); p != "" {
			out = append(out, p)
		} else {
			// Without the right to read its path, as with some games' anti
			// cheat, at least its file name.
			out = append(out, windows.UTF16ToString(e.ExeFile[:]))
		}
	}
	return out
}

var (
	procIsWindowVisible          = windows.NewLazySystemDLL("user32.dll").NewProc("IsWindowVisible")
	procInternalGetWindowText    = windows.NewLazySystemDLL("user32.dll").NewProc("InternalGetWindowText")
	procGetWindowThreadProcessId = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowThreadProcessId")
)

// windowExecutables are the paths of the programs with a window shown, with
// a title, as those a person uses rather than those in the background, but
// Linkcord. The titles are read without sending the windows messages, which
// a window that does not answer, or Linkcord's own, would wait on.
func windowExecutables() []string {
	self := windows.GetCurrentProcessId()
	pids := make(map[uint32]bool)
	var title [2]uint16
	cb := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
		if pid == 0 || pid == self || pids[pid] {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); v == 0 {
			return 1
		}
		if n, _, _ := procInternalGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))); n == 0 {
			return 1
		}
		pids[pid] = true
		return 1
	})
	windows.EnumWindows(cb, nil)
	var out []string
	for pid := range pids {
		if p := processPath(pid); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
