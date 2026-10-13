package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func hideCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func launchPlan(ctx context.Context, p *Plan) error {
	log, err := os.OpenFile(p.Log+".stderr", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", p.Script)
	hideCommand(cmd)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-timeout.C:
			return fail(errors.New("update helper did not become ready; see " + p.Log))
		case <-ticker.C:
			if _, err := os.Stat(p.Ready); err == nil {
				return cmd.Process.Release()
			}
		}
	}
}
