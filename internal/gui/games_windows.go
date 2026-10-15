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
