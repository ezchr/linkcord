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

// windowsName is the name people know their Windows by, with its build:
// "Windows 11 (26100)". Windows 11 still says it is 10.0, and only its
// build, 22000 or later, tells it apart.
func windowsName() string {
	v := windows.RtlGetVersion()
	return windowsNameOf(v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func windowsNameOf(major, minor, build uint32) string {
	name := fmt.Sprintf("Windows %d.%d", major, minor)
	switch {
	case major == 10 && build >= 22000:
		name = "Windows 11"
	case major == 10:
		name = "Windows 10"
	case major == 6 && minor == 3:
		name = "Windows 8.1"
	case major == 6 && minor == 2:
		name = "Windows 8"
	case major == 6 && minor == 1:
		name = "Windows 7"
	}
	return fmt.Sprintf("%s (%d)", name, build)
}
