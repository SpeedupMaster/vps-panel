package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/store"
)

// ---------- zstatic node directory ----------

//go:embed assets/zstatic.json
var zstaticSnapshot []byte

const zstaticAPI = "https://www.zstaticcdn.com/api/v1/DescribeAllNodes"

type zstaticNodes struct {
	UpdatedAt     string `json:"UpdatedAt"`
	ProvinceNodes []struct {
		Province string            `json:"province"`
		Carriers map[string]string `json:"carriers"`
	} `json:"ProvinceNodes"`
	CityNodes []struct {
		City     string `json:"city"`
		Province string `json:"province"`
		Carrier  string `json:"carrier"`
		Endpoint string `json:"endpoint"`
	} `json:"CityNodes"`
}

var carrierCN = map[string]string{"telecom": "电信", "unicom": "联通", "mobile": "移动"}

// DefaultProvinces are enabled on first sync (15 targets: 5 regions × 3 carriers).
var DefaultProvinces = []string{"北京", "上海", "广东", "江苏", "四川"}

func zstaticID(endpoint string) (id, host string, port int) {
	host, ps, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", "", 0
	}
	port, _ = strconv.Atoi(ps)
	prefix := strings.TrimSuffix(strings.SplitN(host, ".", 2)[0], "-v4")
	return "zs-" + prefix, host, port
}

// SyncZstatic refreshes the backhaul target directory from zstaticcdn.com,
// falling back to the embedded snapshot when the API is unreachable and the
// table is empty. Enabled flags of existing targets are kept.
func (p *Panel) SyncZstatic() (int, error) {
	data, err := fetchZstatic()
	src := "api"
	if err != nil {
		var n int64
		p.St.DB.Model(&store.ProbeTarget{}).Where("source = ?", "zstatic").Count(&n)
		if n > 0 {
			return 0, err
		}
		data, src = zstaticSnapshot, "snapshot"
	}
	var z zstaticNodes
	if err := json.Unmarshal(data, &z); err != nil {
		return 0, err
	}
	var existing []store.ProbeTarget
	p.St.DB.Where("source = ?", "zstatic").Find(&existing)
	known := map[string]bool{}
	for _, t := range existing {
		known[t.ID] = t.Enabled
	}
	first := len(existing) == 0
	isDefault := map[string]bool{}
	for _, d := range DefaultProvinces {
		isDefault[d] = true
	}
	count := 0
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		upsert := func(t store.ProbeTarget) error {
			if en, ok := known[t.ID]; ok {
				t.Enabled = en
			} else {
				t.Enabled = first && t.City == "" && isDefault[t.Province]
			}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&t).Error; err != nil {
				return err
			}
			count++
			return nil
		}
		for _, pn := range z.ProvinceNodes {
			for carrier, ep := range pn.Carriers {
				id, host, port := zstaticID(ep)
				if id == "" {
					continue
				}
				if err := upsert(store.ProbeTarget{ID: id, Name: pn.Province + " " + carrierCN[carrier], Carrier: carrier, Province: pn.Province, Host: host, Port: port, Type: "tcp", Source: "zstatic"}); err != nil {
					return err
				}
			}
		}
		for _, cn := range z.CityNodes {
			id, host, port := zstaticID(cn.Endpoint)
			if id == "" {
				continue
			}
			if err := upsert(store.ProbeTarget{ID: id, Name: cn.City + " " + carrierCN[cn.Carrier], Carrier: cn.Carrier, Province: cn.Province, City: cn.City, Host: host, Port: port, Type: "tcp", Source: "zstatic"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	_ = p.St.Set("zstatic_synced_at", time.Now().Format(time.RFC3339)+" ("+src+", 更新于 "+z.UpdatedAt+")")
	p.PushProbeTasksAll()
	return count, nil
}

func fetchZstatic() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, zstaticAPI, nil)
	req.Header.Set("User-Agent", "vps-panel")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("zstatic 返回 %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var z zstaticNodes
	if json.Unmarshal(b, &z) != nil || len(z.ProvinceNodes) == 0 {
		return nil, fmt.Errorf("zstatic 数据格式异常")
	}
	return b, nil
}

// ---------- probe target CRUD ----------

// ErrNotFound indicates a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// ValidateProbeTarget validates a custom probe target and rejects private/loopback hosts.
func ValidateProbeTarget(t *store.ProbeTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.Host = strings.TrimSpace(t.Host)
	if t.Name == "" || t.Host == "" {
		return errors.New("名称和地址必填")
	}
	if t.Type == "" {
		t.Type = "tcp"
	}
	if t.Type != "tcp" && t.Type != "icmp" {
		return errors.New("类型只能是 tcp 或 icmp")
	}
	if t.Type == "tcp" && (t.Port <= 0 || t.Port > 65535) {
		return errors.New("TCP 端口无效")
	}
	if strings.ContainsAny(t.Host, " \t\r\n/\\") {
		return errors.New("地址无效")
	}
	lower := strings.ToLower(t.Host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return errors.New("不能探测本地或内网地址")
	}
	if ip := net.ParseIP(t.Host); ip != nil {
		if notify.IsBlockedIP(ip) {
			return errors.New("不能探测本地或内网地址")
		}
		return nil
	}
	if !strings.Contains(t.Host, ".") {
		return errors.New("地址无效")
	}
	return nil
}

func (p *Panel) ListProbeTargets(source string) []store.ProbeTarget {
	var ts []store.ProbeTarget
	q := p.St.R().Order("source, province, carrier, id")
	if source != "" {
		q = q.Where("source = ?", source)
	}
	q.Find(&ts)
	return ts
}

func (p *Panel) CreateProbeTarget(t *store.ProbeTarget) error {
	if err := ValidateProbeTarget(t); err != nil {
		return err
	}
	t.ID = fmt.Sprintf("cu-%d", nextTaskID()%1e9)
	t.Source = "custom"
	t.Enabled = true
	if err := p.St.DB.Create(t).Error; err != nil {
		return err
	}
	p.PushProbeTasksAll()
	return nil
}

func (p *Panel) UpdateProbeTarget(tid string, in store.ProbeTarget) (*store.ProbeTarget, error) {
	var cur store.ProbeTarget
	if err := p.St.DB.First(&cur, "id = ?", tid).Error; err != nil {
		return nil, ErrNotFound
	}
	if cur.Source == "custom" {
		in.ID, in.Source = cur.ID, cur.Source
		if err := ValidateProbeTarget(&in); err != nil {
			return nil, err
		}
		cur = in
	} else {
		cur.Enabled = in.Enabled // zstatic targets: only toggle
	}
	if err := p.St.DB.Save(&cur).Error; err != nil {
		return nil, err
	}
	p.PushProbeTasksAll()
	return &cur, nil
}

func (p *Panel) DeleteProbeTarget(tid string) error {
	if err := p.St.DB.Where("id = ? AND source = ?", tid, "custom").Delete(&store.ProbeTarget{}).Error; err != nil {
		return err
	}
	p.ForgetProbeTarget(tid)
	p.PushProbeTasksAll()
	return nil
}

// SetProbeTargetsBulk atomically enables exactly the given target IDs.
func (p *Panel) SetProbeTargetsBulk(enabled []string) error {
	if len(enabled) > 60 {
		return errors.New("最多同时启用 60 个目标")
	}
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.ProbeTarget{}).Where("1 = 1").Update("enabled", false).Error; err != nil {
			return err
		}
		if len(enabled) > 0 {
			if err := tx.Model(&store.ProbeTarget{}).Where("id IN ?", enabled).Update("enabled", true).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	p.PushProbeTasksAll()
	return nil
}

// ---------- push targets to agents & diagnostic args ----------

var egressTargets = []proto.ProbeTarget{
	{ID: "eg-gstatic", Kind: "egress", Type: "http", URL: "https://www.gstatic.com/generate_204"},
	{ID: "eg-cloudflare", Kind: "egress", Type: "http", URL: "https://www.cloudflare.com/cdn-cgi/trace"},
	{ID: "eg-apple", Kind: "egress", Type: "http", URL: "https://www.apple.com/library/test/success.html"},
}

// ProbeTargetsFor lists what a server should probe.
func (p *Panel) ProbeTargetsFor(serverID uint) []proto.ProbeTarget {
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return nil
	}
	var ts []store.ProbeTarget
	p.St.DB.Where("enabled = ?", true).Order("id").Find(&ts)
	var out []proto.ProbeTarget
	for _, t := range ts {
		out = append(out, proto.ProbeTarget{ID: t.ID, Kind: "backhaul", Type: t.Type, Host: t.Host, Port: t.Port})
	}
	if sv.CapReality {
		landings := map[uint]bool{}
		for _, r := range p.entryRoutes(serverID) {
			for _, l := range landingsOf(&r) {
				landings[l] = true
			}
		}
		for l := range landings {
			var lsv store.Server
			var li store.LandingInbound
			if p.St.DB.First(&lsv, l).Error != nil || p.St.DB.First(&li, "server_id = ?", l).Error != nil || landingAddr(&lsv, &li) == "" {
				continue
			}
			out = append(out, proto.ProbeTarget{ID: fmt.Sprintf("seg-%d", l), Kind: "segment", Type: "tcp", Host: landingAddr(&lsv, &li), Port: li.Port})
		}
	}
	if sv.CapLanding {
		out = append(out, egressTargets...)
	}
	if p.St.GetBool("probe_peer", false) {
		var peers []store.Server
		p.St.DB.Where("id <> ? AND ipv4 <> ''", serverID).Find(&peers)
		for _, pe := range peers {
			out = append(out, proto.ProbeTarget{ID: fmt.Sprintf("peer-%d", pe.ID), Kind: "peer", Type: "icmp", Host: pe.IPv4})
		}
	}
	return out
}

func (p *Panel) PushProbeTasks(serverID uint) {
	if !p.Hub.Online(serverID) {
		return
	}
	_ = p.Hub.Send(serverID, &proto.PanelMessage{ProbeTasks: &proto.ProbeTasks{
		IntervalSec: p.St.GetInt("probe_interval", 60),
		Count:       p.St.GetInt("probe_count", 5),
		Targets:     p.ProbeTargetsFor(serverID),
	}})
}

func (p *Panel) PushProbeTasksAll() {
	for _, id := range p.Hub.OnlineIDs() {
		p.PushProbeTasks(id)
	}
}

// DiagnoseArgs builds the "我连不上了" task args for a server.
func (p *Panel) DiagnoseArgs(serverID uint) map[string]string {
	var ports []string
	var ri store.RealityInbound
	if p.St.DB.First(&ri, "server_id = ?", serverID).Error == nil {
		ports = append(ports, strconv.Itoa(ri.Port))
	}
	var li store.LandingInbound
	if p.St.DB.First(&li, "server_id = ?", serverID).Error == nil {
		ports = append(ports, strconv.Itoa(li.Port))
	}
	var ts []store.ProbeTarget
	p.St.DB.Where("enabled = ?", true).Limit(9).Find(&ts)
	var targets []string
	for _, t := range ts {
		targets = append(targets, fmt.Sprintf("%s=%s:%d", t.Name, t.Host, t.Port))
	}
	return map[string]string{"ports": strings.Join(ports, ","), "targets": strings.Join(targets, ",")}
}

// TraceArgs picks one target per carrier (Beijing/Shanghai/Guangdong first).
func (p *Panel) TraceArgs() map[string]string {
	var ts []store.ProbeTarget
	p.St.DB.Where("source = ? AND city = ''", "zstatic").Find(&ts)
	pick := map[string]string{}
	for _, pref := range []string{"上海", "广东", "北京"} {
		for _, t := range ts {
			if t.Province == pref && pick[t.Carrier] == "" {
				pick[t.Carrier] = t.Host
			}
		}
	}
	var parts []string
	for _, c := range []string{"telecom", "unicom", "mobile"} {
		if h := pick[c]; h != "" {
			parts = append(parts, c+"="+h)
		}
	}
	return map[string]string{"targets": strings.Join(parts, ",")}
}
