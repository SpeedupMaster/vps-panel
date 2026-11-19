package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/scheduler"
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

// RunIperf starts iperf3 on the target as server and on the source as client.
func (p *Panel) RunIperf(from, to uint) (uint64, error) {
	var dst store.Server
	if err := p.St.DB.First(&dst, to).Error; err != nil || dst.IPv4 == "" {
		return 0, errors.New("对端没有 IPv4")
	}
	port := strconv.Itoa(45201 + int(time.Now().Unix()%50))
	if _, err := p.CreateTask(to, proto.TaskIperf, map[string]string{"role": "server", "port": port}, nil, 90, ""); err != nil {
		return 0, err
	}
	t, err := p.CreateTask(from, proto.TaskIperf, map[string]string{"host": dst.IPv4, "port": port, "peer": strconv.Itoa(int(to))}, nil, 90, "")
	if err != nil {
		return 0, err
	}
	return t.ID, nil
}

// syncNetState copies the congestion control / qdisc reported by a network
// task into the server's system info. That info otherwise only refreshes
// when the agent reconnects, so the tools page kept showing the old values.
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
		if len(q.Sources) == 0 { // results from agents before multi-source
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
	p.St.DB.Where("server_id = ? AND id IN (SELECT MAX(id) FROM tool_results WHERE server_id = ? GROUP BY tool)", serverID, serverID).Find(&rs)
	out := map[string]store.ToolResult{}
	for _, r := range rs {
		out[r.Tool] = r
	}
	return out
}

// ---------- scripts ----------

// BuiltinScripts cover the "system init" items of the plan; they run through
// the exec task, so the VPS must enable allow_remote_exec locally.
var BuiltinScripts = []store.Script{
	{Name: "时区设为 Asia/Shanghai 并开启时间同步", Content: `set -e
timedatectl set-timezone Asia/Shanghai 2>/dev/null || ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
if command -v timedatectl >/dev/null; then timedatectl set-ntp true || true; fi
command -v chronyd >/dev/null || (apt-get install -y -qq chrony 2>/dev/null || dnf install -y -q chrony 2>/dev/null || apk add -q chrony 2>/dev/null || true)
date`},
	{Name: "添加 1G Swap（已有则跳过）", Content: `set -e
if swapon --show | grep -q .; then echo "已有 swap"; swapon --show; exit 0; fi
fallocate -l 1G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=1024
chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
sysctl -w vm.swappiness=10 && echo 'vm.swappiness=10' > /etc/sysctl.d/98-swap.conf
free -h`},
	{Name: "提高文件句柄上限（ulimit）", Content: `set -e
cat > /etc/security/limits.d/99-vpspanel.conf <<'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF
mkdir -p /etc/systemd/system.conf.d
printf '[Manager]\nDefaultLimitNOFILE=1048576\n' > /etc/systemd/system.conf.d/99-vpspanel.conf
systemctl daemon-reexec 2>/dev/null || true
echo "完成，新登录会话生效"`},
	{Name: "安装 fail2ban（保护 SSH）", Content: `set -e
command -v fail2ban-server >/dev/null || (apt-get update -qq && apt-get install -y -qq fail2ban) || dnf install -y -q fail2ban || apk add -q fail2ban
cat > /etc/fail2ban/jail.d/sshd-vpspanel.conf <<'EOF'
[sshd]
enabled = true
maxretry = 5
findtime = 10m
bantime = 1h
EOF
(systemctl enable --now fail2ban && systemctl restart fail2ban) || rc-service fail2ban restart
fail2ban-client status sshd || true`},
	{Name: "SSH 加固：禁用密码登录（确认已配置密钥！）", Content: `set -e
if [ ! -s /root/.ssh/authorized_keys ]; then echo "root 没有 authorized_keys，为防止锁死已中止"; exit 1; fi
mkdir -p /etc/ssh/sshd_config.d
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/99-vpspanel.conf
grep -q '^Include /etc/ssh/sshd_config.d' /etc/ssh/sshd_config || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config
sshd -t && (systemctl reload sshd 2>/dev/null || systemctl reload ssh 2>/dev/null || rc-service sshd reload)
echo "已禁用密码登录"`},
	{Name: "安装 XanMod 内核（BBRv3，需重启，有风险）", Content: `set -e
# Optional per the plan: a new kernel can fail to boot on some providers.
# Make sure the provider offers a VNC/rescue console before running this.
. /etc/os-release
case "$ID" in debian|ubuntu) ;; *) echo "仅支持 Debian / Ubuntu，当前 $ID"; exit 1;; esac
[ "$(uname -m)" = "x86_64" ] || { echo "仅支持 x86_64"; exit 1; }
virt=$(systemd-detect-virt 2>/dev/null || echo none)
case "$virt" in lxc|openvz|docker|podman|wsl) echo "$virt 容器无法更换内核"; exit 1;; esac
flags=$(grep -m1 '^flags' /proc/cpuinfo)
echo "$flags" | grep -qw sse4_2 || { echo "CPU 不支持 x86-64-v2，无法安装 XanMod"; exit 1; }
level=x64v3
for f in avx2 bmi2 fma movbe; do echo "$flags" | grep -qw $f || { level=x64v2; break; }; done
apt-get update -qq && apt-get install -y -qq gnupg wget ca-certificates
install -d -m 0755 /etc/apt/keyrings
wget -qO - https://dl.xanmod.org/archive.key | gpg --dearmor --yes -o /etc/apt/keyrings/xanmod-archive-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/xanmod-archive-keyring.gpg] http://deb.xanmod.org releases main' > /etc/apt/sources.list.d/xanmod-release.list
apt-get update -qq && apt-get install -y -qq linux-xanmod-$level
echo "已安装 linux-xanmod-$level。确认无误后手动重启：reboot"
echo "重启后在面板「工具 → 网络调优」里开启 BBR + fq，内核会使用 BBRv3"`},
}

// SeedScripts adds each built-in script once, so upgrades pick up new ones
// while scripts the user deleted stay deleted.
func (p *Panel) SeedScripts() {
	seeded := map[string]bool{}
	_ = json.Unmarshal([]byte(p.St.Get("scripts_seeded", "")), &seeded)
	if len(seeded) == 0 { // panels from before this list: everything present counts
		var have []store.Script
		p.St.DB.Select("name").Find(&have)
		for _, s := range have {
			seeded[s.Name] = true
		}
		if len(have) > 0 {
			for _, s := range BuiltinScripts[:5] { // the original set
				seeded[s.Name] = true
			}
		}
	}
	for _, s := range BuiltinScripts {
		if seeded[s.Name] {
			continue
		}
		s := s
		if p.SaveScript(&s) == nil {
			seeded[s.Name] = true
		}
	}
	b, _ := json.Marshal(seeded)
	_ = p.St.Set("scripts_seeded", string(b))
}

func (p *Panel) SaveScript(s *store.Script) error {
	if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Content) == "" {
		return errors.New("名称和内容必填")
	}
	if len(s.Content) > 64<<10 {
		return errors.New("脚本过大")
	}
	s.Content = strings.ReplaceAll(s.Content, "\r\n", "\n")
	sum := sha256.Sum256([]byte(s.Content))
	s.SHA256 = hex.EncodeToString(sum[:])
	return p.St.DB.Save(s).Error
}

// RunScript sends a saved script (or ad-hoc content when id is 0) to servers.
func (p *Panel) RunScript(id uint, content string, serverIDs []uint, timeout int) (string, []uint64, error) {
	if id != 0 {
		var s store.Script
		if err := p.St.DB.First(&s, id).Error; err != nil {
			return "", nil, err
		}
		content = s.Content
	}
	if strings.TrimSpace(content) == "" || len(serverIDs) == 0 {
		return "", nil, errors.New("缺少脚本或服务器")
	}
	if timeout <= 0 || timeout > 3600 {
		timeout = 300
	}
	sum := sha256.Sum256([]byte(content))
	batch := fmt.Sprintf("script-%d", time.Now().UnixNano())
	var ids []uint64
	for _, sid := range serverIDs {
		t, err := p.CreateTask(sid, proto.TaskExec, map[string]string{"sha256": hex.EncodeToString(sum[:])}, []byte(content), timeout, batch)
		if err != nil {
			return batch, ids, err
		}
		ids = append(ids, t.ID)
	}
	return batch, ids, nil
}

// ---------- schedules ----------

// ScheduleActions are what a schedule may run.
var ScheduleActions = map[string]string{
	"restart_xray": "重启 Xray", "update_geo": "更新规则文件", "unlock": "解锁检测", "ip_quality": "IP 质量检测",
	"speedtest": "测速", "diagnose": "诊断", "trace": "回程路由",
}

func (p *Panel) scheduleServers(spec string) []uint {
	var svs []store.Server
	q := p.St.DB.Model(&store.Server{})
	switch spec {
	case "", "all":
	case "nodes":
		q = q.Where("mode = ?", "node")
	case "landings":
		q = q.Where("cap_landing = ?", true)
	case "entries":
		q = q.Where("cap_reality = ?", true)
	default:
		q = q.Where("id IN ?", parseIDs(spec))
	}
	q.Find(&svs)
	var ids []uint
	for _, sv := range svs {
		ids = append(ids, sv.ID)
	}
	return ids
}

// ValidateSchedule checks the cron spec: "HH:MM" daily, "HH:MM@1,3,5"
// weekdays (0 = Sunday) or "@every 6h".
func ValidateSchedule(s *store.Schedule) error {
	if s.Name == "" {
		return errors.New("名称必填")
	}
	if strings.HasPrefix(s.Action, "script:") {
		if _, err := strconv.Atoi(strings.TrimPrefix(s.Action, "script:")); err != nil {
			return errors.New("脚本无效")
		}
	} else if ScheduleActions[s.Action] == "" {
		return errors.New("动作无效")
	}
	if _, _, err := parseCron(s.Cron); err != nil {
		return err
	}
	return nil
}

type cronSpec struct {
	every    time.Duration
	hm       int
	weekdays map[time.Weekday]bool
}

func parseCron(c string) (*cronSpec, bool, error) {
	c = strings.TrimSpace(c)
	if d, ok := strings.CutPrefix(c, "@every "); ok {
		dur, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil || dur < 10*time.Minute {
			return nil, false, errors.New("间隔至少 10 分钟，例如 @every 6h")
		}
		return &cronSpec{every: dur}, true, nil
	}
	hm, days, _ := strings.Cut(c, "@")
	h, m, ok := scheduler.ParseHM(hm)
	if !ok {
		return nil, false, errors.New("时间格式应为 HH:MM、HH:MM@1,3,5 或 @every 6h")
	}
	cs := &cronSpec{hm: h*60 + m}
	if days != "" {
		cs.weekdays = map[time.Weekday]bool{}
		for _, d := range strings.Split(days, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(d))
			if err != nil || n < 0 || n > 6 {
				return nil, false, errors.New("星期取值 0-6")
			}
			cs.weekdays[time.Weekday(n)] = true
		}
	}
	return cs, false, nil
}

// due reports whether a schedule should run at now (checked every minute).
func scheduleDue(s *store.Schedule, now time.Time) bool {
	cs, interval, err := parseCron(s.Cron)
	if err != nil {
		return false
	}
	if interval {
		return now.Sub(s.LastRun) >= cs.every-30*time.Second
	}
	if cs.weekdays != nil && !cs.weekdays[now.Weekday()] {
		return false
	}
	return now.Hour()*60+now.Minute() == cs.hm && now.Sub(s.LastRun) > 2*time.Minute
}

// RunSchedules is called every minute.
func (p *Panel) RunSchedules() {
	var ss []store.Schedule
	p.St.DB.Where("enabled = ?", true).Find(&ss)
	now := time.Now()
	for i := range ss {
		if scheduleDue(&ss[i], now) {
			p.St.DB.Model(&ss[i]).Update("last_run", now)
			p.RunScheduleNow(&ss[i])
		}
	}
}

func (p *Panel) RunScheduleNow(s *store.Schedule) {
	ids := p.scheduleServers(s.Servers)
	if sid, ok := strings.CutPrefix(s.Action, "script:"); ok {
		id, _ := strconv.Atoi(sid)
		_, _, _ = p.RunScript(uint(id), "", ids, 0)
		return
	}
	for _, id := range ids {
		if Tools[s.Action].Node {
			var sv store.Server
			if p.St.DB.Select("mode").First(&sv, id).Error != nil || sv.Mode != "node" {
				continue
			}
		}
		_, _ = p.RunTool(id, s.Action, nil)
	}
}

// RunToolSync runs a tool and waits (used by tests / the route wizard).
func (p *Panel) RunToolSync(ctx context.Context, serverID uint, tool string) (*proto.TaskResult, string, error) {
	def, ok := Tools[tool]
	if !ok {
		return nil, "", errors.New("未知工具")
	}
	return p.RunTask(ctx, serverID, def.Task, nil, nil, def.Timeout)
}
