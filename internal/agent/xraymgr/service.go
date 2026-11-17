package xraymgr

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
export XRAY_LOCATION_ASSET=/usr/local/share/xray
depend() { need net; }
`

func installService() error {
	if systemd() {
		if err := os.WriteFile("/etc/systemd/system/xray.service", []byte(unitFile), 0o644); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return exec.Command("systemctl", "enable", "xray").Run()
	}
	if err := os.WriteFile("/etc/init.d/xray", []byte(openrcFile), 0o755); err != nil {
		return err
	}
	return exec.Command("rc-update", "add", "xray", "default").Run()
}

func removeService() {
	if systemd() {
		_ = exec.Command("systemctl", "disable", "--now", "xray").Run()
		_ = os.Remove("/etc/systemd/system/xray.service")
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return
	}
	_ = exec.Command("rc-service", "xray", "stop").Run()
	_ = exec.Command("rc-update", "del", "xray", "default").Run()
	_ = os.Remove("/etc/init.d/xray")
}

func ServiceActive() bool {
	if systemd() {
		return exec.Command("systemctl", "is-active", "--quiet", "xray").Run() == nil
	}
	return exec.Command("rc-service", "xray", "status").Run() == nil
}

func RestartService(ctx context.Context) error {
	if systemd() {
		return exec.CommandContext(ctx, "systemctl", "restart", "xray").Run()
	}
	return exec.CommandContext(ctx, "rc-service", "xray", "restart").Run()
}

func StopService(ctx context.Context) error {
	if systemd() {
		return exec.CommandContext(ctx, "systemctl", "stop", "xray").Run()
	}
	return exec.CommandContext(ctx, "rc-service", "xray", "stop").Run()
}

// RecentLog returns the last n lines of Xray's log.
func RecentLog(ctx context.Context, n int) string {
	if systemd() {
		b, _ := exec.CommandContext(ctx, "journalctl", "-u", "xray", "-n", strconv.Itoa(n), "--no-pager", "-o", "cat").CombinedOutput()
		return strings.TrimSpace(string(b))
	}
	b, _ := exec.CommandContext(ctx, "tail", "-n", strconv.Itoa(n), "/var/log/messages").CombinedOutput()
	return strings.TrimSpace(string(b))
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
