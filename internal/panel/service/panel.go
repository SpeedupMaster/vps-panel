// Package service holds the panel's business logic.
package service

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

// Alerter is implemented by the alert engine.
type Alerter interface {
	Fire(kind string, serverID uint, key, severity, text string)
	Resolve(kind string, serverID uint, key string)
	Info(kind string, serverID uint, text string) // one-shot notification
	// Observe feeds a metric into threshold rules of that kind.
	Observe(kind string, serverID uint, value float64)
}

type nopAlerter struct{}

func (nopAlerter) Fire(string, uint, string, string, string) {}
func (nopAlerter) Resolve(string, uint, string)              {}
func (nopAlerter) Info(string, uint, string)                 {}
func (nopAlerter) Observe(string, uint, float64)             {}

type Panel struct {
	St      *store.Store
	Box     *secret.Box
	Auth    *auth.Auth
	Hub     *hub.Hub
	Bus     *Bus
	Alert   Alerter
	DataDir string

	mu              sync.RWMutex
	latest          map[uint]*proto.Metrics
	waiters         map[uint64][]chan *proto.TaskResult
	bundledAgentDir string

	deployMu    sync.Mutex
	deployLocks map[uint]*sync.Mutex
}

func New(st *store.Store, box *secret.Box, a *auth.Auth, h *hub.Hub, dataDir string) *Panel {
	p := &Panel{
		St: st, Box: box, Auth: a, Hub: h, Bus: NewBus(), Alert: nopAlerter{}, DataDir: dataDir,
		latest:      map[uint]*proto.Metrics{},
		waiters:     map[uint64][]chan *proto.TaskResult{},
		deployLocks: map[uint]*sync.Mutex{},
	}
	h.SetHandler(p)
	return p
}

func (p *Panel) serverDeployLock(serverID uint) *sync.Mutex {
	p.deployMu.Lock()
	defer p.deployMu.Unlock()
	if p.deployLocks == nil {
		p.deployLocks = map[uint]*sync.Mutex{}
	}
	m := p.deployLocks[serverID]
	if m == nil {
		m = &sync.Mutex{}
		p.deployLocks[serverID] = m
	}
	return m
}

func (p *Panel) deleteDeployLock(serverID uint) {
	p.deployMu.Lock()
	delete(p.deployLocks, serverID)
	p.deployMu.Unlock()
}

// Latest returns the most recent metrics frame for a server.
func (p *Panel) Latest(id uint) *proto.Metrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.latest[id]
}

// ---------- hub.Handler ----------

func (p *Panel) OnConnect(c *hub.Conn) {
	var sv store.Server
	if err := p.St.DB.First(&sv, c.ServerID).Error; err != nil {
		return
	}
	h := c.Hello()
	sys, _ := json.Marshal(h.System)
	sw, _ := json.Marshal(h.Switches)
	upd := map[string]any{
		"status": "online", "last_seen_at": time.Now(), "agent_version": h.Version,
		"system_json": string(sys), "switches_json": string(sw),
	}
	if h.System.IPv4 != "" {
		if sv.IPv4 != "" && sv.IPv4 != h.System.IPv4 {
			p.onIPChanged(&sv, sv.IPv4, h.System.IPv4)
		}
		upd["ipv4"] = h.System.IPv4
	}
	if h.System.IPv6 != "" {
		upd["ipv6"] = h.System.IPv6
	}
	p.St.DB.Model(&sv).Updates(upd)
	log.Printf("agent %s (%s) online from %s, v%s", sv.Name, c.AgentID, c.RemoteIP, h.Version)
	p.Alert.Resolve("offline", sv.ID, "offline")
	p.Bus.Publish("server", map[string]any{"id": sv.ID, "status": "online"})
	p.pushAgentSetup(sv.ID)
	go p.flushPendingTasks(sv.ID)
}

func (p *Panel) OnDisconnect(c *hub.Conn) {
	p.St.DB.Model(&store.Server{}).Where("id = ?", c.ServerID).Update("status", "suspect")
	p.Bus.Publish("server", map[string]any{"id": c.ServerID, "status": "suspect"})
}

func (p *Panel) OnMessage(c *hub.Conn, m *proto.AgentMessage) {
	id := c.ServerID
	switch {
	case m.Metrics != nil:
		p.ingestMetrics(id, m.Metrics)
	case m.Traffic != nil:
		p.ingestTraffic(c, m.Traffic)
	case m.Probe != nil:
		p.ingestProbe(id, m.Probe)
	case m.TaskResult != nil:
		p.onTaskResult(id, m.TaskResult)
	case m.XrayState != nil:
		p.onXrayState(id, m.XrayState)
	case m.Event != nil:
		p.onEvent(id, m.Event)
	}
}

// pushAgentSetup sends settings and probe tasks after (re)connect.
func (p *Panel) pushAgentSetup(serverID uint) {
	_ = p.Hub.Send(serverID, &proto.PanelMessage{Settings: p.agentSettings()})
	p.PushProbeTasks(serverID)
}

func (p *Panel) onEvent(serverID uint, e *proto.Event) {
	name := p.ServerName(serverID)
	switch e.Kind {
	case "xray_crash":
		p.Alert.Fire("xray_crash", serverID, "xray_crash", "critical", name+" Xray 进程退出："+e.Detail)
	case "xray_restart":
		p.Alert.Resolve("xray_crash", serverID, "xray_crash")
		p.Alert.Info("xray_restart", serverID, name+" Xray 已自动重启")
	case "config_drift":
		p.St.DB.Model(&store.Server{}).Where("id = ?", serverID).Update("xray_drift", true)
		p.Alert.Info("config_drift", serverID, name+" 的 Xray 配置被本地修改（配置漂移）")
	case "rollback":
		p.Alert.Info("rollback", serverID, name+" 已自动回滚："+e.Detail)
	}
	p.Audit("system", "agent_event", name, e.Kind+": "+e.Detail, "")
}

func (p *Panel) ServerName(id uint) string {
	var sv store.Server
	if err := p.St.DB.Select("name").First(&sv, id).Error; err != nil {
		return "#?"
	}
	return sv.Name
}

// Audit records an operation in the audit log.
func (p *Panel) Audit(typ, action, target, detail, ip string) {
	p.St.DB.Create(&store.AuditLog{Type: typ, Action: action, Target: target, Detail: detail, IP: ip, TS: time.Now()})
}
