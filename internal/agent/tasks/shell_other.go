//go:build !linux

package tasks

import (
	"context"
	"errors"

	"vpspanel/internal/common/proto"
)

func Shell(ctx context.Context, in <-chan *proto.ShellInput, out func(string)) error {
	return errors.New("WebSSH 仅支持 Linux")
}
