//go:build !windows

package update

import (
	"context"
	"errors"
	"os/exec"
)

func hideCommand(*exec.Cmd) {}

func launchPlan(context.Context, *Plan) error {
	return errors.New("automatic executable replacement is supported on Windows only")
}
