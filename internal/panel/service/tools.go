package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// Tools maps UI tool names to agent task types and default timeouts.
var Tools = map[string]struct {
	Task    string
	Timeout int
	Node    bool // needs Xray
	Save    bool // keep the result in tool_results
}{
	"diagnose":      {proto.TaskDiagnose, 120, false, true},
	"unlock":        {proto.TaskUnlock, 180, false, true},
	"ip_quality":    {proto.TaskIPQuality, 120, false, true},
	"trace":         {proto.TaskTrace, 300, false, true},
	"speedtest":     {proto.TaskSpeedtest, 180, false, true},
	"reality_scan":  {proto.TaskRealityScan, 180, false, true},
	"net_status":    {proto.TaskNetStatus, 30, false, false},
	"tune_net":      {proto.TaskTuneNet, 300, false, true},
	"restore_net":   {proto.TaskRestoreNet, 60, false, false},
	"xray_log":      {proto.TaskXrayLog, 30, true, false},
	"restart_xray":  {proto.TaskRestartXray, 60, true, false},
	"restart_agent": {proto.TaskRestartAgent, 30, false, false},
	"update_geo":    {proto.TaskUpdateGeo, 300, true, false},
}

// RunTool starts a tool task and returns its id. iperf is handled by RunIperf.
func (p *Panel) RunTool(serverID uint, tool string, args map[string]string) (uint64, error) {
	def, ok := Tools[tool]
	if !ok {
		return 0, errors.New("未知工具")
	}
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return 0, err
	}
	if def.Node && sv.Mode != "node" {
		return 0, errors.New("仅节点机可用")
	}
	if args == nil {
		args = map[string]string{}
	}
	var payload []byte
	switch tool {
	case "ip_quality":
		// API keys go in the payload, which is never returned by the task API.
		if k := p.SecretSetting("ipapi_is_key"); k != "" {
			payload, _ = json.Marshal(proto.IPQualityKeys{IPAPIIs: k})
		}
	case "diagnose":
		for k, v := range p.DiagnoseArgs(serverID) {
			args[k] = v
		}
	case "trace":
		if args["targets"] == "" {
			args["targets"] = p.TraceArgs()["targets"]
		}
	case "tune_net":
		if args["rtt_ms"] == "" {
			if bh := p.backhaulAvg(serverID); bh > 0 {
				args["rtt_ms"] = strconv.Itoa(int(bh))
			}
		}
	case "update_geo":
		if args["source"] == "" {
			args["source"] = p.St.Get("geo_source", "loyalsoldier")
		}
	}
	t, err := p.CreateTask(serverID, def.Task, args, payload, def.Timeout, "")
	if err != nil {
		return 0, err
	}
	return t.ID, nil
}

const (
	iperfPortMin = 45201
	iperfPortMax = 45300
	iperfPortTTL = 2 * time.Minute
)

func (p *Panel) allocIperfPort() (int, error) {
	p.iperfMu.Lock()
	defer p.iperfMu.Unlock()
	if p.iperfInUse == nil {
		p.iperfInUse = map[int]time.Time{}
	}
	now := time.Now()
	for port, exp := range p.iperfInUse {
		if now.After(exp) {
			delete(p.iperfInUse, port)
		}
	}
	span := iperfPortMax - iperfPortMin + 1
	for i := 0; i < span; i++ {
		port := iperfPortMin + (p.iperfNext+i)%span
		if _, busy := p.iperfInUse[port]; !busy {
			p.iperfInUse[port] = now.Add(iperfPortTTL)
			p.iperfNext = (p.iperfNext + i + 1) % span
			return port, nil
		}
	}
	return 0, errors.New("当前测速任务过多，请稍后再试")
}

func (p *Panel) releaseIperfPort(port int) {
	if port < iperfPortMin || port > iperfPortMax {
		return
	}
	p.iperfMu.Lock()
	delete(p.iperfInUse, port)
	p.iperfMu.Unlock()
}

// RunIperf starts iperf3 on the target as server and on the source as client.
func (p *Panel) RunIperf(from, to uint) (uint64, error) {
	var dst store.Server
	if err := p.St.R().First(&dst, to).Error; err != nil || dst.IPv4 == "" {
		return 0, errors.New("对端没有 IPv4")
	}
	portNum, err := p.allocIperfPort()
	if err != nil {
		return 0, err
	}
	port := strconv.Itoa(portNum)
	if _, err := p.CreateTask(to, proto.TaskIperf, map[string]string{"role": "server", "port": port}, nil, 90, ""); err != nil {
		p.releaseIperfPort(portNum)
		return 0, err
	}
	if p.Hub.Online(to) {
		time.Sleep(150 * time.Millisecond)
	}
	t, err := p.CreateTask(from, proto.TaskIperf, map[string]string{"host": dst.IPv4, "port": port, "peer": strconv.Itoa(int(to))}, nil, 90, "")
	if err != nil {
		p.releaseIperfPort(portNum)
		return 0, err
	}
	return t.ID, nil
}

// syncNetState copies the congestion control / qdisc reported by a network
// task into the server's system info.
func (p *Panel) syncNetState(t *store.Task, r *proto.TaskResult) {
	if t.Type != proto.TaskTuneNet && t.Type != proto.TaskRestoreNet && t.Type != proto.TaskNetStatus || r.Data == nil {
		return
	}
	type netState struct{ Congestion, Qdisc, Available string }
	var d struct {
		netState
		Status *netState `json:"status"`
	}
	b, _ := json.Marshal(r.Data)
	if json.Unmarshal(b, &d) != nil {
		return
	}
	ns := d.netState
	if d.Status != nil {
		ns = *d.Status
	}
	if ns.Congestion == "" {
		return
	}
	var sv store.Server
	if p.St.DB.First(&sv, t.ServerID).Error != nil {
		return
	}
	sys := SystemInfo(&sv)
	sys.Congestion, sys.Qdisc, sys.AvailCC = ns.Congestion, ns.Qdisc, ns.Available
	sj, _ := json.Marshal(sys)
	p.St.DB.Model(&sv).Update("system_json", string(sj))
	p.Bus.Publish("server", map[string]any{"id": sv.ID, "system": sys})
}

// afterToolTask keeps finished tool results for the tools page / route wizard.
func (p *Panel) afterToolTask(t *store.Task, r *proto.TaskResult) {
	p.syncNetState(t, r)
	var tool string
	for name, def := range Tools {
		if def.Task == t.Type && def.Save {
			tool = name
		}
	}
	if t.Type == proto.TaskIperf {
		var args map[string]string
		_ = json.Unmarshal([]byte(t.Args), &args)
		if args["role"] == "server" {
			return
		}
		if portNum, err := strconv.Atoi(args["port"]); err == nil {
			p.releaseIperfPort(portNum)
		}
		tool = "iperf"
	}
	if tool == "" || r.Status != "success" || r.Data == nil {
		return
	}
	b, _ := json.Marshal(r.Data)
	p.St.DB.Create(&store.ToolResult{ServerID: t.ServerID, Tool: tool, Summary: toolSummary(tool, b), Data: string(b), TS: time.Now()})
	p.Bus.Publish("tool", map[string]any{"server_id": t.ServerID, "tool": tool})
}

func toolSummary(tool string, data []byte) string {
	switch tool {
	case "unlock":
		var items []struct{ Name, Status, Region string }
		_ = json.Unmarshal(data, &items)
		var yes []string
		for _, it := range items {
			if it.Status == "yes" {
				s := it.Name
				if it.Region != "" {
					s += "(" + it.Region + ")"
				}
				yes = append(yes, s)
			}
		}
		if len(yes) == 0 {
			return "均未解锁"
		}
		return "解锁 " + strings.Join(yes, " ")
	case "ip_quality":
		var q struct {
			Type      string
			Risk      int
			Blacklist []string
			Checked   int
			Sources   []struct {
				Name    string
				Flags   []string
				Error   string
				Skipped string
			}
		}
		_ = json.Unmarshal(data, &q)
		if len(q.Sources) == 0 {
			return fmt.Sprintf("%s，风险 %d，黑名单 %d", q.Type, q.Risk, len(q.Blacklist))
		}
		var parts, flags []string
		seen := map[string]bool{}
		failed, riskKnown := 0, false
		for _, s := range q.Sources {
			if s.Skipped != "" {
				continue
			}
			if s.Error != "" {
				failed++
				continue
			}
			if s.Name == "proxycheck.io" {
				riskKnown = true
			}
			for _, f := range s.Flags {
				if !seen[f] {
					seen[f] = true
					flags = append(flags, f)
				}
			}
		}
		if q.Type != "" {
			parts = append(parts, q.Type)
		}
		if riskKnown {
			parts = append(parts, fmt.Sprintf("风险 %d", q.Risk))
		} else {
			parts = append(parts, "风险未知")
		}
		if len(flags) > 0 {
			parts = append(parts, strings.Join(flags, "/"))
		}
		if q.Checked > 0 {
			parts = append(parts, fmt.Sprintf("黑名单 %d", len(q.Blacklist)))
		}
		if failed > 0 {
			parts = append(parts, fmt.Sprintf("%d 个来源查询失败", failed))
		}
		return strings.Join(parts, "，")
	case "trace":
		var rs []struct{ Carrier, Line string }
		_ = json.Unmarshal(data, &rs)
		var parts []string
		for _, r := range rs {
			parts = append(parts, carrierCN[r.Carrier]+" "+r.Line)
		}
		return strings.Join(parts, "，")
	case "speedtest":
		var s struct {
			DownMbps float64 `json:"down_mbps"`
			UpMbps   float64 `json:"up_mbps"`
			PingMs   float64 `json:"ping_ms"`
		}
		_ = json.Unmarshal(data, &s)
		return fmt.Sprintf("↓%.0f ↑%.0f Mbps，%.0f ms", s.DownMbps, s.UpMbps, s.PingMs)
	case "iperf":
		var s struct{ Mbps float64 }
		_ = json.Unmarshal(data, &s)
		return fmt.Sprintf("%.0f Mbps", s.Mbps)
	case "diagnose":
		var items []struct{ Level, Name string }
		_ = json.Unmarshal(data, &items)
		var bad []string
		for _, it := range items {
			if it.Level == "fail" {
				bad = append(bad, it.Name)
			}
		}
		if len(bad) == 0 {
			return "未发现问题"
		}
		return "异常：" + strings.Join(bad, "、")
	case "reality_scan":
		var cs []struct {
			Domain string
			OK     bool
			CDN    string
		}
		_ = json.Unmarshal(data, &cs)
		n, cdn := 0, 0
		for _, c := range cs {
			if c.OK {
				n++
			}
			if c.CDN != "" {
				cdn++
			}
		}
		if cdn > 0 {
			return fmt.Sprintf("%d/%d 个可用，%d 个走 CDN", n, len(cs), cdn)
		}
		return fmt.Sprintf("%d/%d 个可用", n, len(cs))
	}
	return ""
}

// LatestToolResults returns the newest result of each tool for a server.
func (p *Panel) LatestToolResults(serverID uint) map[string]store.ToolResult {
	var rs []store.ToolResult
	p.St.R().Where("server_id = ? AND id IN (SELECT MAX(id) FROM tool_results WHERE server_id = ? GROUP BY tool)", serverID, serverID).Find(&rs)
	out := map[string]store.ToolResult{}
	for _, r := range rs {
		out[r.Tool] = r
	}
	return out
}

// ToolHistory returns recent saved tool results filtered by optional tool and server ID.
func (p *Panel) ToolHistory(tool, serverID string, limit int) []store.ToolResult {
	if limit <= 0 {
		limit = 200
	}
	q := p.St.R().Order("id desc").Limit(limit)
	if tool != "" {
		q = q.Where("tool = ?", tool)
	}
	if serverID != "" {
		q = q.Where("server_id = ?", serverID)
	}
	var rs []store.ToolResult
	q.Find(&rs)
	return rs
}

// RunToolSync runs a tool and waits (used by tests / the route wizard).
func (p *Panel) RunToolSync(ctx context.Context, serverID uint, tool string) (*proto.TaskResult, string, error) {
	def, ok := Tools[tool]
	if !ok {
		return nil, "", errors.New("未知工具")
	}
	return p.RunTask(ctx, serverID, def.Task, nil, nil, def.Timeout)
}
