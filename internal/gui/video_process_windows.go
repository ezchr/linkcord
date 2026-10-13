package gui

import (
	"os/exec"
	"syscall"
)

func hideMediaProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
