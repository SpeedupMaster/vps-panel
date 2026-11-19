package xraymgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var (
	systemdUnitPath = "/etc/systemd/system/xray.service"
	openrcInitPath  = "/etc/init.d/xray"
	openrcLogPath   = "/var/log/xray.log"
)

func systemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

const unitFile = `[Unit]
Description=Xray (managed by vps-panel)
After=network-online.target
Wants=network-online.target

[Service]
User=root
NoNewPrivileges=true
Environment=XRAY_LOCATION_ASSET=/usr/local/share/xray
ExecStart=/usr/local/bin/xray run -c /usr/local/etc/xray/config.json
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

const openrcFile = `#!/sbin/openrc-run
name="xray"
command="/usr/local/bin/xray"
command_args="run -c /usr/local/etc/xray/config.json"
command_background=true
pidfile="/run/xray.pid"
output_log="/var/log/xray.log"
error_log="/var/log/xray.log"
export XRAY_LOCATION_ASSET=/usr/local/share/xray
depend() { need net; }
`

func serviceInstalled() bool {
	if systemd() {
		_, err := os.Stat(systemdUnitPath)
		return err == nil
	}
	_, err := os.Stat(openrcInitPath)
	return err == nil
}

func ensureService() error {
	if serviceInstalled() {
		return nil
	}
	return installService()
}

func runServiceCmd(ctx context.Context, name string, args ...string) error {
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(b)); msg != "" {
			return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
		}
		return err
	}
	return nil
}

func installService() error {
	if systemd() {
		if err := os.WriteFile(systemdUnitPath, []byte(unitFile), 0o644); err != nil {
			return err
		}
		if err := runServiceCmd(context.Background(), "systemctl", "daemon-reload"); err != nil {
			return err
		}
		return runServiceCmd(context.Background(), "systemctl", "enable", "xray")
	}
	if err := os.WriteFile(openrcInitPath, []byte(openrcFile), 0o755); err != nil {
		return err
	}
	return runServiceCmd(context.Background(), "rc-update", "add", "xray", "default")
}

func removeService() {
	if systemd() {
		_ = exec.Command("systemctl", "disable", "--now", "xray").Run()
		_ = os.Remove(systemdUnitPath)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return
	}
	_ = exec.Command("rc-service", "xray", "stop").Run()
	_ = exec.Command("rc-update", "del", "xray", "default").Run()
	_ = os.Remove(openrcInitPath)
}

func ServiceActive() bool {
	if systemd() {
		return exec.Command("systemctl", "is-active", "--quiet", "xray").Run() == nil
	}
	return exec.Command("rc-service", "xray", "status").Run() == nil
}

func RestartService(ctx context.Context) error {
	if systemd() {
		return runServiceCmd(ctx, "systemctl", "restart", "xray")
	}
	return runServiceCmd(ctx, "rc-service", "xray", "restart")
}

func StopService(ctx context.Context) error {
	if systemd() {
		return runServiceCmd(ctx, "systemctl", "stop", "xray")
	}
	return runServiceCmd(ctx, "rc-service", "xray", "stop")
}

// RecentLog returns the last n lines of Xray's log.
func RecentLog(ctx context.Context, n int) string {
	if systemd() {
		b, _ := exec.CommandContext(ctx, "journalctl", "-u", "xray", "-n", strconv.Itoa(n), "--no-pager", "-o", "cat").CombinedOutput()
		return strings.TrimSpace(string(b))
	}
	logPath := openrcLogPath
	if _, err := os.Stat(logPath); err != nil {
		logPath = "/var/log/messages"
	}
	b, _ := exec.CommandContext(ctx, "tail", "-n", strconv.Itoa(n), logPath).CombinedOutput()
	return strings.TrimSpace(string(b))
}

// startErrDetail combines the service manager error with recent Xray logs so
// startup failures never return an empty reason.
func startErrDetail(ctx context.Context, restartErr error) string {
	var parts []string
	if restartErr != nil {
		parts = append(parts, restartErr.Error())
	}
	if logs := RecentLog(ctx, 15); logs != "" {
		parts = append(parts, logs)
	} else if systemd() {
		if b, _ := exec.CommandContext(ctx, "systemctl", "status", "xray", "--no-pager", "-n", "10").CombinedOutput(); len(b) > 0 {
			if s := strings.TrimSpace(string(b)); s != "" {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return "服务未保持运行状态（无日志输出）"
	}
	return strings.Join(parts, "\n")
}

// mainPID returns Xray's PID or 0.
func mainPID() int {
	if systemd() {
		b, err := exec.Command("systemctl", "show", "-p", "MainPID", "--value", "xray").Output()
		if err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return pid
		}
	}
	if b, err := os.ReadFile("/run/xray.pid"); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return pid
	}
	return 0
}
