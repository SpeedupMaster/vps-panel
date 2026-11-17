// Package tasks implements the agent-side task handlers.
package tasks

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"vpspanel/internal/agent"
	"vpspanel/internal/common/proto"
)

var errRemoteExecOff = errors.New("这台机器未开启远程执行：在 VPS 上执行 vpsagent config set allow_remote_exec true 后重启 Agent")

// Register installs all handlers on the agent.
func Register(a *agent.Agent) {
	a.Tasks[proto.TaskRestartAgent] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		out("Agent 将在 2 秒后重启\n")
		go func() {
			time.Sleep(2 * time.Second)
			os.Exit(0) // systemd / OpenRC restarts us
		}()
		return nil, nil
	}
	a.Tasks[proto.TaskNetStatus] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return NetStatus(), nil
	}
	a.Tasks[proto.TaskTuneNet] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !a.Local.Switches.AllowSystemTune {
			return nil, errors.New("本机 agent.yaml 中 allow_system_tune 为 false")
		}
		return TuneNet(t.Args["profile"], t.Args["bandwidth_mbps"], t.Args["rtt_ms"], t.Args["dry_run"] == "1", out)
	}
	a.Tasks[proto.TaskRestoreNet] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !a.Local.Switches.AllowSystemTune {
			return nil, errors.New("本机 agent.yaml 中 allow_system_tune 为 false")
		}
		return RestoreNet(out)
	}
	a.Tasks[proto.TaskExec] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !a.Local.Switches.AllowRemoteExec {
			return nil, errRemoteExecOff
		}
		code, err := RunStream(ctx, out, "/bin/sh", "-c", string(t.Payload))
		return map[string]int{"exit_code": code}, err
	}
	a.Tasks[proto.TaskShell] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !a.Local.Switches.AllowRemoteExec {
			return nil, errRemoteExecOff
		}
		in := a.RegisterShell(t.ID)
		defer a.UnregisterShell(t.ID)
		return nil, Shell(ctx, in, out)
	}
	a.Tasks[proto.TaskDiagnose] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return Diagnose(ctx, a, t.Args, out), nil
	}
	a.Tasks[proto.TaskUnlock] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return Unlock(ctx, out), nil
	}
	a.Tasks[proto.TaskIPQuality] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return IPQuality(ctx, out)
	}
	a.Tasks[proto.TaskTrace] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return Trace(ctx, t.Args, out)
	}
	a.Tasks[proto.TaskSpeedtest] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return Speedtest(ctx, out)
	}
	a.Tasks[proto.TaskIperf] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return Iperf(ctx, t.Args, out)
	}
	a.Tasks[proto.TaskRealityScan] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		return RealityScan(ctx, t.Args["candidates"], out), nil
	}
	a.Tasks[proto.TaskUpdateAgent] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !a.Local.Switches.AllowSelfUpdate {
			return nil, errors.New("本机 agent.yaml 中 allow_self_update 为 false")
		}
		return nil, UpdateAgent(ctx, t.Args, out)
	}
	registerXray(a)
}

// RunStream runs a command and streams combined output.
func RunStream(ctx context.Context, out func(string), name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			out(sc.Text() + "\n")
		}
	}()
	err := cmd.Wait()
	pw.Close()
	wg.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return code, err
}

// Output runs a command and returns its combined output.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(b), err
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
