package gui

import (
	"syscall"
)

var procNotifyAddrChange = syscall.NewLazyDLL("iphlpapi.dll").NewProc("NotifyAddrChange")

// waitAddrChange blocks until an IP address of the computer changes.
func waitAddrChange() bool {
	// With no handle and no overlapped, NotifyAddrChange blocks until a
	// change and returns NO_ERROR.
	r, _, _ := procNotifyAddrChange.Call(0, 0)
	return r == 0
}
