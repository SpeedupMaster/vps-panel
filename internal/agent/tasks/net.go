package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"vpspanel/internal/agent/collector"
	"vpspanel/internal/agent/config"
)

const sysctlFile = "/etc/sysctl.d/99-vpspanel.conf"

func sysctlPath(key string) string {
	return "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
}

func readSysctl(key string) string {
	b, _ := os.ReadFile(sysctlPath(key))
	return strings.Join(strings.Fields(string(b)), " ")
}

func writeSysctl(key, val string) error {
	return os.WriteFile(sysctlPath(key), []byte(val), 0o644)
}

var tunedKeys = []string{
	"net.core.default_qdisc", "net.ipv4.tcp_congestion_control",
	"net.core.rmem_max", "net.core.wmem_max", "net.ipv4.tcp_rmem", "net.ipv4.tcp_wmem",
	"net.ipv4.tcp_fastopen", "net.ipv4.tcp_mtu_probing", "net.ipv4.tcp_slow_start_after_idle",
	"net.ipv4.tcp_notsent_lowat", "net.core.somaxconn", "net.ipv4.ip_local_port_range",
}

type NetInfo struct {
	Kernel     string            `json:"kernel"`
	Virt       string            `json:"virt"`
	Congestion string            `json:"congestion"`
	Qdisc      string            `json:"qdisc"`
	Available  string            `json:"available"`
	Tuned      bool              `json:"tuned"`
	Supported  bool              `json:"supported"`
	Reason     string            `json:"reason,omitempty"`
	Values     map[string]string `json:"values"`
}

func kernelAtLeast(major, minor int) bool {
	k := readFirst("/proc/sys/kernel/osrelease")
	parts := strings.SplitN(k, ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, _ := strconv.Atoi(parts[0])
	mi, _ := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	return ma > major || (ma == major && mi >= minor)
}

func readFirst(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func NetStatus() *NetInfo {
	n := &NetInfo{
		Kernel: readFirst("/proc/sys/kernel/osrelease"), Virt: collector.DetectVirt(),
		Congestion: readSysctl("net.ipv4.tcp_congestion_control"), Qdisc: readSysctl("net.core.default_qdisc"),
		Available: readSysctl("net.ipv4.tcp_available_congestion_control"), Values: map[string]string{},
	}
	_, err := os.Stat(sysctlFile)
	n.Tuned = err == nil
	for _, k := range tunedKeys {
		n.Values[k] = readSysctl(k)
	}
	n.Supported = true
	switch {
	case n.Virt == "openvz" || n.Virt == "lxc" || n.Virt == "docker":
		n.Supported = false
		n.Reason = n.Virt + " 容器共享宿主机内核，拥塞控制和 sysctl 只能由商家修改"
	case !kernelAtLeast(4, 9):
		n.Supported = false
		n.Reason = "内核版本低于 4.9，不支持 BBR"
	}
	return n
}

// bufferSize returns the TCP buffer cap for a profile from bandwidth × RTT.
func bufferSize(profile string, bwMbps, rttMs float64) int {
	if bwMbps <= 0 {
		bwMbps = 1000
	}
	if rttMs <= 0 {
		rttMs = 180
	}
	bdp := bwMbps * 1e6 / 8 * rttMs / 1000
	factor, lo, hi := 2.0, 8<<20, 32<<20
	switch profile {
	case "conservative":
		factor, lo, hi = 1, 4<<20, 16<<20
	case "aggressive":
		factor, lo, hi = 4, 16<<20, 64<<20
	}
	size := int(bdp * factor)
	return max(lo, min(hi, size))
}

type TuneResult struct {
	Diff    []string `json:"diff"`
	Applied bool     `json:"applied"`
}

// TuneNet enables BBR + fq and sizes TCP buffers. The previous values are
// saved once so RestoreNet can put them back.
func TuneNet(profile, bw, rtt string, dryRun bool, out func(string)) (*TuneResult, error) {
	st := NetStatus()
	if !st.Supported {
		return nil, errors.New(st.Reason)
	}
	bwF, _ := strconv.ParseFloat(bw, 64)
	rttF, _ := strconv.ParseFloat(rtt, 64)
	buf := bufferSize(profile, bwF, rttF)
	want := map[string]string{
		"net.core.default_qdisc":             "fq",
		"net.ipv4.tcp_congestion_control":    "bbr",
		"net.core.rmem_max":                  strconv.Itoa(buf),
		"net.core.wmem_max":                  strconv.Itoa(buf),
		"net.ipv4.tcp_rmem":                  fmt.Sprintf("4096 131072 %d", buf),
		"net.ipv4.tcp_wmem":                  fmt.Sprintf("4096 65536 %d", buf),
		"net.ipv4.tcp_fastopen":              "3",
		"net.ipv4.tcp_mtu_probing":           "1",
		"net.ipv4.tcp_slow_start_after_idle": "0",
		"net.ipv4.tcp_notsent_lowat":         "16384",
		"net.core.somaxconn":                 "4096",
		"net.ipv4.ip_local_port_range":       "1024 65535",
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := &TuneResult{}
	for _, k := range keys {
		cur := readSysctl(k)
		if cur != want[k] {
			res.Diff = append(res.Diff, fmt.Sprintf("- %s = %s", k, cur), fmt.Sprintf("+ %s = %s", k, want[k]))
		}
	}
	if dryRun {
		return res, nil
	}
	if !strings.Contains(st.Available, "bbr") {
		out("加载 tcp_bbr 模块\n")
		_ = exec.Command("modprobe", "tcp_bbr").Run()
	}
	backup := filepath.Join(config.DataDir, "sysctl-backup.json")
	if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
		b, _ := json.MarshalIndent(st.Values, "", "  ")
		if err := os.WriteFile(backup, b, 0o600); err != nil {
			return nil, err
		}
		out("已备份原始参数\n")
	}
	var sb strings.Builder
	sb.WriteString("# 由 vps-panel 生成，profile=" + profile + "；还原请在面板操作，或删除本文件后重启\n")
	for _, k := range keys {
		sb.WriteString(k + " = " + want[k] + "\n")
		if err := writeSysctl(k, want[k]); err != nil {
			out(fmt.Sprintf("设置 %s 失败：%v\n", k, err))
		}
	}
	if err := os.WriteFile(sysctlFile, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}
	out("已写入 " + sysctlFile + "\n")
	if got := readSysctl("net.ipv4.tcp_congestion_control"); got != "bbr" {
		return res, fmt.Errorf("拥塞控制仍为 %s，内核可能未编译 BBR", got)
	}
	res.Applied = true
	return res, nil
}

func RestoreNet(out func(string)) (any, error) {
	backup := filepath.Join(config.DataDir, "sysctl-backup.json")
	b, err := os.ReadFile(backup)
	if err != nil {
		return nil, errors.New("没有找到优化前的备份")
	}
	var vals map[string]string
	if err := json.Unmarshal(b, &vals); err != nil {
		return nil, err
	}
	for k, v := range vals {
		if v == "" {
			continue
		}
		if err := writeSysctl(k, v); err != nil {
			out(fmt.Sprintf("还原 %s 失败：%v\n", k, err))
		}
	}
	_ = os.Remove(sysctlFile)
	_ = os.Remove(backup)
	out("已还原并删除 " + sysctlFile + "\n")
	return NetStatus(), nil
}
