package tasks

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/agent"
	"vpspanel/internal/agent/prober"
)

// DiagItem is one line of the "我连不上了" report.
type DiagItem struct {
	Name   string `json:"name"`
	Level  string `json:"level"` // ok | warn | fail
	Detail string `json:"detail"`
}

// Diagnose checks Xray, listening ports, firewall, backhaul reachability,
// clock skew, DNS and recent logs.
//
//	args["ports"]   = "443,8443"            ports Xray should listen on
//	args["targets"] = "telecom=host:80,..." backhaul probe targets
func Diagnose(ctx context.Context, a *agent.Agent, args map[string]string, out func(string)) []DiagItem {
	var items []DiagItem
	add := func(name, level, detail string) {
		items = append(items, DiagItem{name, level, detail})
		mark := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[level]
		out(fmt.Sprintf("[%s] %s：%s\n", mark, name, detail))
	}

	// Xray
	if a.Xray != nil {
		st := a.Xray.State()
		switch {
		case !st.Installed:
			add("Xray", "fail", "未安装")
		case !st.Running:
			add("Xray", "fail", "已安装但未运行（"+st.Version+"）")
		default:
			p := a.Xray.Proc()
			add("Xray", "ok", fmt.Sprintf("运行中 %s，PID %d，已运行 %s", st.Version, p.PID, (time.Duration(p.Uptime)*time.Second).String()))
		}
		if st.Drift {
			add("配置", "warn", "本地配置文件被改动过，与面板最后下发的版本不一致")
		} else if st.Installed {
			add("配置", "ok", fmt.Sprintf("与面板一致（版本 v%d）", st.ConfigVer))
		}
	} else {
		add("Xray", "ok", "仅探针模式，未安装节点")
	}

	// listening ports
	listening := listeningPorts()
	for _, ps := range strings.Split(args["ports"], ",") {
		ps = strings.TrimSpace(ps)
		if ps == "" {
			continue
		}
		port, _ := strconv.Atoi(ps)
		if listening[port] {
			add("端口 "+ps, "ok", "正在监听")
			if d, err := prober.TCPPing("127.0.0.1", port, 2*time.Second); err == nil {
				add("本机连接 "+ps, "ok", fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000))
			}
		} else {
			add("端口 "+ps, "fail", "没有进程在监听")
		}
	}

	// firewall
	if have("ufw") {
		if s, _ := Output(ctx, "ufw", "status"); strings.Contains(s, "Status: active") {
			add("防火墙", "warn", "ufw 已启用，请确认入站端口已放行")
		}
	}
	if have("firewall-cmd") {
		if s, _ := Output(ctx, "firewall-cmd", "--state"); strings.TrimSpace(s) == "running" {
			add("防火墙", "warn", "firewalld 运行中，请确认入站端口已放行")
		}
	}

	// backhaul: TCP to domestic targets. All-failed while foreign sites work
	// strongly suggests the IP is blocked.
	ok, total := 0, 0
	var parts []string
	for _, pair := range strings.Split(args["targets"], ",") {
		name, hp, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found {
			continue
		}
		host, ps, _ := net.SplitHostPort(hp)
		port, _ := strconv.Atoi(ps)
		total++
		var rtts []float64
		for i := 0; i < 3; i++ {
			if d, err := prober.TCPPing(host, port, 3*time.Second); err == nil {
				rtts = append(rtts, float64(d.Microseconds())/1000)
			}
		}
		s := prober.Summarize(name, rtts, 3)
		if s.Loss < 100 {
			ok++
			parts = append(parts, fmt.Sprintf("%s %.0fms", name, s.Avg))
		} else {
			parts = append(parts, name+" 超时")
		}
	}
	intl := false
	if _, err := prober.TCPPing("1.1.1.1", 443, 3*time.Second); err == nil {
		intl = true
	}
	switch {
	case total == 0:
	case ok == 0 && intl:
		add("回程", "fail", "国内目标全部不通但国外正常，IP 很可能被封："+strings.Join(parts, "，"))
	case ok == 0:
		add("回程", "fail", "全部不通，且国外也不通，检查 VPS 网络："+strings.Join(parts, "，"))
	case ok < total:
		add("回程", "warn", strings.Join(parts, "，"))
	default:
		add("回程", "ok", strings.Join(parts, "，"))
	}
	if !intl {
		add("出站", "fail", "无法连接 1.1.1.1:443")
	}

	// DNS
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	if _, err := net.DefaultResolver.LookupHost(c, "www.apple.com"); err != nil {
		add("DNS", "fail", err.Error())
	} else {
		add("DNS", "ok", "解析正常")
	}
	cancel()

	// clock skew matters for Reality / VMess
	if resp, _, err := get(ctx, "https://www.cloudflare.com/cdn-cgi/trace", true); err == nil {
		if t, err := time.Parse(time.RFC1123, resp.Header.Get("Date")); err == nil {
			skew := time.Since(t).Round(time.Second)
			if skew < 0 {
				skew = -skew
			}
			if skew > 90*time.Second {
				add("时间", "fail", "系统时间偏差 "+skew.String()+"，Reality 握手会失败")
			} else {
				add("时间", "ok", "偏差 "+skew.String())
			}
		}
	}

	// resources
	m, sys := a.Col.Sample(), a.Col.System()
	if sys.DiskTotal > 0 && float64(m.DiskUsed)/float64(sys.DiskTotal) > 0.95 {
		add("磁盘", "fail", "磁盘使用率超过 95%")
	}
	if sys.MemTotal > 0 && float64(m.MemUsed)/float64(sys.MemTotal) > 0.95 {
		add("内存", "warn", "内存使用率超过 95%")
	}

	// recent logs
	if a.Xray != nil && have("journalctl") {
		if s, err := Output(ctx, "journalctl", "-u", "xray", "-n", "30", "--no-pager", "-o", "cat"); err == nil {
			out("\n---- 最近 30 行 Xray 日志 ----\n" + s)
		}
	}
	return items
}

// listeningPorts reads /proc/net/tcp{,6} for LISTEN sockets.
func listeningPorts() map[int]bool {
	res := map[int]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n")[1:] {
			fs := strings.Fields(l)
			if len(fs) < 4 || fs[3] != "0A" {
				continue
			}
			if i := strings.LastIndex(fs[1], ":"); i >= 0 {
				if p, err := strconv.ParseInt(fs[1][i+1:], 16, 32); err == nil {
					res[int(p)] = true
				}
			}
		}
	}
	return res
}
