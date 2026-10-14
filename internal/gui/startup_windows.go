package gui

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKey is where Windows keeps what it opens when the user signs in.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// startsWithWindows reports whether Windows opens Linkcord when the user
// signs in.
func startsWithWindows() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Linkcord")
	return err == nil
}

// setStartWithWindows has Windows open this Linkcord when the user signs
// in, minimized if asked, or not.
func setStartWithWindows(on, minimized bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("Linkcord"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := `"` + exe + `"`
	if minimized {
		cmd += " --minimized"
	}
	return k.SetStringValue("Linkcord", cmd)
}
