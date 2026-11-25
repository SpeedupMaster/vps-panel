//go:build !linux

package tasks

import (
	"context"
	"errors"
	"os/exec"

	"vpspanel/internal/common/proto"
)

func setProcGroup(cmd *exec.Cmd) {}

func killProcGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func Shell(ctx context.Context, in <-chan *proto.ShellInput, out func(string)) error {
	return errors.New("WebSSH 仅支持 Linux")
}
