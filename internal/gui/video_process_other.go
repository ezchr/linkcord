//go:build !windows

package gui

import "os/exec"

func hideMediaProcess(cmd *exec.Cmd) {}
