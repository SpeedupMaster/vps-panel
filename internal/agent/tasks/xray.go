package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"vpspanel/internal/agent"
	"vpspanel/internal/agent/config"
	"vpspanel/internal/agent/xraymgr"
	"vpspanel/internal/common/proto"
)

func restartSoon() {
	go func() {
		time.Sleep(3 * time.Second)
		os.Exit(0) // the service manager restarts us in the new mode
	}()
}

func registerXray(a *agent.Agent) {
	mgr, _ := a.Xray.(*xraymgr.Manager)
	if mgr == nil {
		mgr = xraymgr.New(config.DataDir) // probe mode: only used by install
	}
	src := func(t *proto.Task) xraymgr.Source {
		return xraymgr.Source{Version: t.Args["version"], BaseURL: t.Args["base_url"]}
	}

	// install_xray also upgrades a probe-only machine to a node.
	a.Tasks[proto.TaskInstallXray] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if err := mgr.Install(ctx, src(t), out); err != nil {
			return nil, err
		}
		if a.Local.Mode != "node" {
			if err := config.SetMode("node"); err != nil {
				return nil, err
			}
			out("已切换为节点模式，Agent 即将重启\n")
			restartSoon()
		}
		return mgr.State(), nil
	}
	// uninstall_xray with args["downgrade"]=1 turns the node back into a probe.
	a.Tasks[proto.TaskUninstallXray] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if err := mgr.Uninstall(ctx, out); err != nil {
			return nil, err
		}
		if t.Args["downgrade"] == "1" && a.Local.Mode == "node" {
			if err := config.SetMode("probe"); err != nil {
				return nil, err
			}
			out("已切换为仅探针模式，Agent 即将重启\n")
			restartSoon()
		}
		return nil, nil
	}
	a.Tasks[proto.TaskUpdateXray] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if err := mgr.Update(ctx, src(t), out); err != nil {
			return nil, err
		}
		return mgr.State(), nil
	}
	a.Tasks[proto.TaskUpdateGeo] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if !xraymgr.Installed() {
			return nil, errors.New("Xray 未安装")
		}
		return nil, mgr.UpdateGeo(ctx, t.Args["source"], out)
	}
	a.Tasks[proto.TaskRestartXray] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		if t.Args["action"] == "stop" { // traffic quota exceeded
			if err := xraymgr.StopService(ctx); err != nil {
				return nil, err
			}
			out("Xray 已停止\n")
			return mgr.State(), nil
		}
		if err := xraymgr.RestartService(ctx); err != nil {
			if logs := xraymgr.RecentLog(ctx, 10); logs != "" {
				return nil, fmt.Errorf("%v\n%s", err, logs)
			}
			return nil, err
		}
		out("Xray 已重启\n")
		return mgr.State(), nil
	}
	a.Tasks[proto.TaskXrayLog] = func(ctx context.Context, t *proto.Task, out func(string)) (any, error) {
		n, _ := strconv.Atoi(t.Args["lines"])
		if n <= 0 || n > 2000 {
			n = 200
		}
		out(xraymgr.RecentLog(ctx, n) + "\n")
		return nil, nil
	}
}
