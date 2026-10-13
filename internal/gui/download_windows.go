package gui

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// knownDownloadsDir is where Windows keeps the Downloads folder, which the
// user may have moved.
func knownDownloadsDir() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil {
		return ""
	}
	return dir
}

// showInFolder opens Explorer with the file selected. Explorer reads its own
// command line, which wants the path quoted after the comma.
func showInFolder(p string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + p + `"`}
	go cmd.Run()
}
