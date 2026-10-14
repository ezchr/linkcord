package gui

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// windowsVersion is the version of Windows, such as 6.1.7601 for Windows 7
// with its service pack, for error reports.
func windowsVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
