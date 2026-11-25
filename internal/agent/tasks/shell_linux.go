//go:build linux

package tasks

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"

	"vpspanel/internal/common/proto"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	return err
}

// Shell runs an interactive login shell on a PTY. Input arrives as
// ShellInput frames; output is streamed as task output. Idle sessions end
// after 15 minutes.
func Shell(ctx context.Context, in <-chan *proto.ShellInput, out func(string)) error {
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		var carry []byte
		for {
			n, err := f.Read(buf)
			if n > 0 {
				data := append(carry, buf[:n]...)
				// Hold back an incomplete UTF-8 sequence for the next read.
				cut := len(data)
				for i := 0; i < 3 && cut > 0; i++ {
					if utf8.RuneStart(data[cut-1]) {
						if !utf8.FullRune(data[cut-1:]) {
							cut--
						}
						break
					}
					cut--
				}
				if cut == 0 || utf8.Valid(data[:cut]) {
					out(string(data[:cut]))
					carry = append([]byte(nil), data[cut:]...)
				} else {
					out(string(data))
					carry = nil
				}
			}
			if err != nil {
				return
			}
		}
	}()

	var reapOnce sync.Once
	reap := func(kill bool) {
		reapOnce.Do(func() {
			if kill && cmd.Process != nil {
				_ = killProcGroup(cmd)
			}
			_ = f.Close()
			_ = cmd.Wait()
			<-done
		})
	}
	defer reap(true)

	idle := time.NewTimer(15 * time.Minute)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			reap(true)
			return nil
		case <-done:
			reap(false)
			return nil
		case <-idle.C:
			out("\r\n[15 分钟无操作，会话已断开]\r\n")
			reap(true)
			return nil
		case m, ok := <-in:
			if !ok {
				reap(true)
				return nil
			}
			idle.Reset(15 * time.Minute)
			if m.Close {
				reap(true)
				return nil
			}
			if m.Cols > 0 && m.Rows > 0 {
				_ = pty.Setsize(f, &pty.Winsize{Cols: uint16(m.Cols), Rows: uint16(m.Rows)})
			}
			if len(m.Data) > 0 {
				_, _ = f.Write(m.Data)
			}
		}
	}
}
