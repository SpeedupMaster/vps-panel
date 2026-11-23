package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/store"
)

// KindLabels names alert kinds in messages and the UI.
var KindLabels = map[string]string{
	"offline": "VPS 离线", "cpu": "CPU", "mem": "内存", "disk": "磁盘", "load": "负载（每核）", "steal": "CPU steal",
	"backhaul_loss": "回程丢包", "backhaul_latency": "回程延迟", "blocked": "疑似 IP 被封", "segment_down": "入口→落地中断",
	"traffic": "流量", "traffic_spike": "流量突增", "expire": "VPS 到期", "xray_crash": "Xray 崩溃", "xray_restart": "Xray 自动重启",
	"config_drift": "配置漂移", "deploy_failed": "配置下发失败", "new_version": "新版本", "agent_outdated": "Agent 版本过旧",
	"stranger_sub": "订阅陌生 IP", "login_failed": "登录失败过多", "login_new_ip": "新 IP 登录", "ip_changed": "IP 变化",
	"rollback": "自动回滚", "failover": "切换到备用落地", "update_result": "批量更新结果", "backup": "备份",
}

var kindUnits = map[string]string{"cpu": "%", "mem": "%", "disk": "%", "steal": "%", "backhaul_loss": "%", "backhaul_latency": " ms", "load": ""}

// DefaultRules are created on first start.
var DefaultRules = []store.AlertRule{
	{Name: "VPS 离线", Kind: "offline", Duration: 90, Cooldown: 3600, Severity: "critical", Scope: "all", Enabled: true},
	{Name: "CPU 持续过高", Kind: "cpu", Threshold: 90, Duration: 300, Cooldown: 3600, Severity: "warning", Scope: "all", Enabled: true},
	{Name: "内存不足", Kind: "mem", Threshold: 90, Duration: 300, Cooldown: 3600, Severity: "warning", Scope: "all", Enabled: true},
	{Name: "磁盘将满", Kind: "disk", Threshold: 90, Duration: 60, Cooldown: 21600, Severity: "critical", Scope: "all", Enabled: true},
	{Name: "负载过高", Kind: "load", Threshold: 2, Duration: 600, Cooldown: 3600, Severity: "warning", Scope: "all", Enabled: true},
	{Name: "CPU steal 过高（超售）", Kind: "steal", Threshold: 20, Duration: 600, Cooldown: 21600, Severity: "warning", Scope: "all", Enabled: true},
	{Name: "回程丢包", Kind: "backhaul_loss", Threshold: 30, Duration: 300, Cooldown: 3600, Severity: "warning", Scope: "node", Enabled: true},
	{Name: "回程延迟过高", Kind: "backhaul_latency", Threshold: 300, Duration: 600, Cooldown: 3600, Severity: "warning", Scope: "node", Enabled: true},
	{Name: "疑似 IP 被封", Kind: "blocked", Severity: "critical", Scope: "all", Enabled: true},
	{Name: "入口→落地中断", Kind: "segment_down", Duration: 180, Cooldown: 3600, Severity: "warning", Scope: "entry", Enabled: true},
	{Name: "切换到备用落地", Kind: "failover", Cooldown: 21600, Severity: "warning", Scope: "entry", Enabled: true},
	{Name: "流量异常突增", Kind: "traffic_spike", Severity: "warning", Scope: "all", Enabled: true},
	{Name: "Agent 版本过旧", Kind: "agent_outdated", Severity: "info", Scope: "all", Enabled: true},
	{Name: "流量额度", Kind: "traffic", Severity: "warning", Scope: "all", Enabled: true},
	{Name: "VPS 即将到期", Kind: "expire", Severity: "info", Scope: "all", Enabled: true},
	{Name: "Xray 崩溃", Kind: "xray_crash", Duration: 30, Cooldown: 3600, Severity: "critical", Scope: "node", Enabled: true},
	{Name: "订阅被陌生 IP 拉取", Kind: "stranger_sub", Severity: "warning", Scope: "all", Enabled: true},
	{Name: "面板登录安全", Kind: "login_new_ip", Severity: "warning", Scope: "all", Enabled: true},
	{Name: "登录失败过多", Kind: "login_failed", Severity: "warning", Scope: "all", Enabled: true},
	{Name: "新版本可用", Kind: "new_version", Severity: "info", Scope: "all", Enabled: true},
}

type pendingMsg struct {
	kind, severity, text string
	recovery             bool
	channels             string
}

type AlertEngine struct {
	p *Panel

	mu        sync.Mutex
	since     map[string]time.Time // Observe: condition start
	queue     []pendingMsg
	ruleCache map[string][]store.AlertRule
}

func NewAlertEngine(p *Panel) *AlertEngine {
	e := &AlertEngine{p: p, since: map[string]time.Time{}}
	var n int64
	p.St.DB.Model(&store.AlertRule{}).Count(&n)
	if n == 0 {
		for _, r := range DefaultRules {
			r := r
			p.St.DB.Create(&r)
		}
	}
	invalidate := func(db *gorm.DB) {
		if db.Statement == nil {
			return
		}
		table := db.Statement.Table
		if table == "" && db.Statement.Schema != nil {
			table = db.Statement.Schema.Table
		}
		if table == "alert_rules" {
			e.InvalidateRules()
		}
	}
	cb := p.St.DB.Callback()
	_ = cb.Create().After("gorm:create").Register("vpspanel:alert_rules_cache_create", invalidate)
	_ = cb.Update().After("gorm:update").Register("vpspanel:alert_rules_cache_update", invalidate)
	_ = cb.Delete().After("gorm:delete").Register("vpspanel:alert_rules_cache_delete", invalidate)
	return e
}

// InvalidateRules clears the cached alert rules so the next lookup reloads from SQLite.
func (e *AlertEngine) InvalidateRules() {
	e.mu.Lock()
	e.ruleCache = nil
	e.mu.Unlock()
}

// Start runs the merge-and-send loop.
func (e *AlertEngine) Start(s *scheduler.Scheduler) {
	s.Every("alert-flush", 10*time.Second, false, e.flush)
}

// ForgetServer drops in-memory duration trackers for a deleted server.
func (e *AlertEngine) ForgetServer(serverID uint) {
	suffix := "|" + strconv.FormatUint(uint64(serverID), 10)
	e.mu.Lock()
	for k := range e.since {
		if strings.HasSuffix(k, suffix) {
			delete(e.since, k)
		}
	}
	e.mu.Unlock()
}

func (e *AlertEngine) rules(kind string) []store.AlertRule {
	e.mu.Lock()
	cache := e.ruleCache
	e.mu.Unlock()
	if cache != nil {
		return cache[kind]
	}
	var all []store.AlertRule
	e.p.St.DB.Find(&all)
	m := make(map[string][]store.AlertRule, len(all))
	for _, r := range all {
		m[r.Kind] = append(m[r.Kind], r)
	}
	e.mu.Lock()
	if e.ruleCache == nil {
		e.ruleCache = m
	}
	rs := e.ruleCache[kind]
	e.mu.Unlock()
	return rs
}

func (e *AlertEngine) matches(r store.AlertRule, serverID uint) bool {
	if serverID == 0 || r.Scope == "" || r.Scope == "all" {
		return true
	}
	var sv store.Server
	if e.p.St.DB.Select("id, mode, cap_reality, cap_landing").First(&sv, serverID).Error != nil {
		return false
	}
	switch {
	case r.Scope == "node":
		return sv.Mode == "node"
	case r.Scope == "probe":
		return sv.Mode == "probe"
	case r.Scope == "entry":
		return sv.CapReality
	case r.Scope == "landing":
		return sv.CapLanding
	case strings.HasPrefix(r.Scope, "ids:"):
		for _, id := range parseIDs(strings.TrimPrefix(r.Scope, "ids:")) {
			if id == serverID {
				return true
			}
		}
	}
	return false
}

// ruleFor returns the first matching rule; ok=false when the kind is
// configured but disabled / out of scope. Unknown kinds use defaults.
func (e *AlertEngine) ruleFor(kind string, serverID uint) (store.AlertRule, bool) {
	rs := e.rules(kind)
	if len(rs) == 0 {
		return store.AlertRule{Kind: kind, Enabled: true}, true
	}
	for _, r := range rs {
		if r.Enabled && e.matches(r, serverID) {
			return r, true
		}
	}
	return store.AlertRule{}, false
}

func (e *AlertEngine) Fire(kind string, serverID uint, key, severity, text string) {
	r, ok := e.ruleFor(kind, serverID)
	if !ok {
		return
	}
	if r.Severity != "" && kind != "traffic" { // traffic picks its level per threshold
		severity = r.Severity
	}
	db := e.p.St.DB
	var ev store.AlertEvent
	now := time.Now()
	if db.Where("kind = ? AND server_id = ? AND key = ? AND status = ?", kind, serverID, key, "firing").First(&ev).Error == nil {
		// Still firing: repeat after the cooldown unless acked / silenced.
		if r.Cooldown > 0 && !ev.Acked && (ev.SilencedTo == nil || now.After(*ev.SilencedTo)) &&
			ev.LastSentAt != nil && now.Sub(*ev.LastSentAt) > time.Duration(r.Cooldown)*time.Second {
			db.Model(&ev).Updates(map[string]any{"last_sent_at": &now, "text": text})
			e.enqueue(pendingMsg{kind: kind, severity: severity, text: text + "（仍未恢复）", channels: r.Channels})
		}
		return
	}
	ev = store.AlertEvent{RuleID: r.ID, Kind: kind, ServerID: serverID, Key: key, Severity: severity, Text: text, Status: "firing", StartedAt: now, LastSentAt: &now}
	db.Create(&ev)
	e.enqueue(pendingMsg{kind: kind, severity: severity, text: text, channels: r.Channels})
	e.p.Bus.Publish("alert", ev)
}

func (e *AlertEngine) Resolve(kind string, serverID uint, key string) {
	db := e.p.St.DB
	var evs []store.AlertEvent
	db.Where("kind = ? AND server_id = ? AND key = ? AND status = ?", kind, serverID, key, "firing").Find(&evs)
	now := time.Now()
	for _, ev := range evs {
		db.Model(&ev).Updates(map[string]any{"status": "resolved", "resolved_at": &now})
		if e.p.St.GetBool("alert_recovery", true) {
			r, _ := e.ruleFor(kind, serverID)
			dur := now.Sub(ev.StartedAt).Round(time.Second)
			e.enqueue(pendingMsg{kind: kind, severity: "info", recovery: true, channels: r.Channels,
				text: fmt.Sprintf("%s 已恢复（持续 %s）", strings.TrimSuffix(firstClause(ev.Text), "："), humanDur(dur))})
		}
		e.p.Bus.Publish("alert", map[string]any{"id": ev.ID, "status": "resolved"})
	}
}

func (e *AlertEngine) Info(kind string, serverID uint, text string) {
	r, ok := e.ruleFor(kind, serverID)
	if !ok {
		return
	}
	sev := r.Severity
	if sev == "" {
		sev = "info"
	}
	now := time.Now()
	ev := store.AlertEvent{RuleID: r.ID, Kind: kind, ServerID: serverID, Key: kind, Severity: sev, Text: text, Status: "info", StartedAt: now, ResolvedAt: &now, LastSentAt: &now}
	e.p.St.DB.Create(&ev)
	e.enqueue(pendingMsg{kind: kind, severity: sev, text: text, channels: r.Channels})
	e.p.Bus.Publish("alert", ev)
}

// Observe implements threshold + duration rules for metric kinds.
func (e *AlertEngine) Observe(kind string, serverID uint, value float64) {
	for _, r := range e.rules(kind) {
		if !r.Enabled || !e.matches(r, serverID) {
			continue
		}
		key := fmt.Sprintf("rule-%d", r.ID)
		sk := fmt.Sprintf("%d|%d", r.ID, serverID)
		if value >= r.Threshold {
			e.mu.Lock()
			start, ok := e.since[sk]
			if !ok {
				start = time.Now()
				e.since[sk] = start
			}
			e.mu.Unlock()
			if time.Since(start) >= time.Duration(r.Duration)*time.Second {
				unit := kindUnits[kind]
				text := fmt.Sprintf("%s %s %.1f%s，超过阈值 %g%s（已持续 %s）", e.p.ServerName(serverID), KindLabels[kind], value, unit, r.Threshold, unit, humanDur(time.Since(start).Round(time.Second)))
				e.Fire(kind, serverID, key, r.Severity, text)
			}
		} else {
			e.mu.Lock()
			_, was := e.since[sk]
			delete(e.since, sk)
			e.mu.Unlock()
			if was {
				e.Resolve(kind, serverID, key)
			}
		}
	}
}

func (e *AlertEngine) enqueue(m pendingMsg) {
	e.mu.Lock()
	e.queue = append(e.queue, m)
	e.mu.Unlock()
}

func inQuiet(spec string, now time.Time) bool {
	a, b, ok := strings.Cut(spec, "-")
	if !ok {
		return false
	}
	ha, ma, ok1 := scheduler.ParseHM(a)
	hb, mb, ok2 := scheduler.ParseHM(b)
	if !ok1 || !ok2 {
		return false
	}
	ta, tb := ha*60+ma, hb*60+mb
	cur := now.Hour()*60 + now.Minute()
	if ta <= tb {
		return cur >= ta && cur < tb
	}
	return cur >= ta || cur < tb // spans midnight
}

var sevRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// flush merges queued messages (e.g. several servers offline at once) and
// sends them to the matching channels.
func (e *AlertEngine) flush() {
	e.mu.Lock()
	q := e.queue
	e.queue = nil
	e.mu.Unlock()
	if len(q) == 0 {
		return
	}
	quiet := inQuiet(e.p.St.Get("alert_quiet", ""), time.Now())
	type group struct {
		kind, sev, channels string
		recovery            bool
		lines               []string
	}
	groups := map[string]*group{}
	var order []string
	for _, m := range q {
		if quiet && m.severity != "critical" {
			continue // quiet hours: only critical alerts get through
		}
		k := m.kind + "|" + m.channels + "|" + strconv.FormatBool(m.recovery)
		g := groups[k]
		if g == nil {
			g = &group{kind: m.kind, sev: m.severity, channels: m.channels, recovery: m.recovery}
			groups[k] = g
			order = append(order, k)
		}
		if sevRank[m.severity] > sevRank[g.sev] {
			g.sev = m.severity
		}
		g.lines = append(g.lines, m.text)
	}
	for _, k := range order {
		g := groups[k]
		title := KindLabels[g.kind]
		if title == "" {
			title = g.kind
		}
		sev := g.sev
		if g.recovery {
			title += " · 已恢复"
			sev = "recovery"
		}
		text := strings.Join(g.lines, "\n")
		if len(g.lines) > 1 {
			title = fmt.Sprintf("%s（%d 条）", title, len(g.lines))
			text = "• " + strings.Join(g.lines, "\n• ")
		}
		e.send(notify.Message{Title: title, Text: text, Severity: sev, Kind: g.kind, TS: time.Now().Unix()}, g.channels)
	}
}

func (e *AlertEngine) send(m notify.Message, channelIDs string) {
	var chs []store.NotifyChannel
	q := e.p.St.DB.Where("enabled = ?", true)
	if ids := parseIDs(channelIDs); len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	q.Find(&chs)
	rank := sevRank[m.Severity]
	if m.Severity == "recovery" {
		rank = 1 // recoveries follow warnings
	}
	for _, ch := range chs {
		if rank < sevRank[ch.MinLevel] {
			continue
		}
		cfg, err := e.p.ChannelConfig(&ch)
		if err != nil {
			continue
		}
		go func(typ, name string, cfg map[string]string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := notify.Send(ctx, typ, cfg, m); err != nil {
				log.Printf("notify %s: %v", name, err)
			}
		}(ch.Type, ch.Name, cfg)
	}
}

// ChannelConfig decrypts a channel's settings.
func (p *Panel) ChannelConfig(ch *store.NotifyChannel) (map[string]string, error) {
	s, err := p.Box.Open(ch.ConfigEnc)
	if err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	return cfg, json.Unmarshal([]byte(s), &cfg)
}

func (p *Panel) SaveChannel(ch *store.NotifyChannel, cfg map[string]string) error {
	if _, ok := notify.Fields[ch.Type]; !ok {
		return fmt.Errorf("未知渠道类型 %s", ch.Type)
	}
	if ch.ID != 0 { // keep secrets the UI didn't resend
		var old store.NotifyChannel
		if p.St.DB.First(&old, ch.ID).Error == nil {
			if prev, err := p.ChannelConfig(&old); err == nil {
				for k, v := range prev {
					if cfg[k] == "" || cfg[k] == "••••••" {
						cfg[k] = v
					}
				}
			}
		}
	}
	b, _ := json.Marshal(cfg)
	ch.ConfigEnc = p.Box.Seal(string(b))
	if ch.MinLevel == "" {
		ch.MinLevel = "info"
	}
	return p.St.DB.Save(ch).Error
}

// TestChannel sends a test message synchronously.
func (p *Panel) TestChannel(id uint) error {
	var ch store.NotifyChannel
	if err := p.St.DB.First(&ch, id).Error; err != nil {
		return err
	}
	cfg, err := p.ChannelConfig(&ch)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return notify.Send(ctx, ch.Type, cfg, notify.Message{Title: "测试通知", Text: "来自 vps-panel 的测试消息，收到说明渠道配置正确。", Severity: "info", Kind: "test", TS: time.Now().Unix()})
}

// checkResourceAlerts feeds metric frames into threshold rules.
func (p *Panel) checkResourceAlerts(serverID uint, m *proto.Metrics) {
	p.Alert.Observe("cpu", serverID, m.CPU)
	p.Alert.Observe("steal", serverID, m.Steal)
	var sys proto.SystemInfo
	if c := p.Hub.Conn(serverID); c != nil && c.Hello() != nil {
		sys = c.Hello().System
	}
	if sys.MemTotal > 0 {
		p.Alert.Observe("mem", serverID, float64(m.MemUsed)/float64(sys.MemTotal)*100)
	}
	if sys.DiskTotal > 0 {
		p.Alert.Observe("disk", serverID, float64(m.DiskUsed)/float64(sys.DiskTotal)*100)
	}
	if sys.CPUCores > 0 {
		p.Alert.Observe("load", serverID, m.Load1/float64(sys.CPUCores))
	}
}

func firstClause(s string) string {
	if i := strings.IndexAny(s, "：（"); i > 0 {
		return s[:i]
	}
	return s
}

func humanDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f 小时", d.Hours())
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}
