package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/clause"

	"vpspanel/internal/common/proto"
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
	upsert := func(t store.ProbeTarget) {
		if en, ok := known[t.ID]; ok {
			t.Enabled = en
		} else {
			t.Enabled = first && t.City == "" && isDefault[t.Province]
		}
		p.St.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(&t)
		count++
	}
	for _, pn := range z.ProvinceNodes {
		for carrier, ep := range pn.Carriers {
			id, host, port := zstaticID(ep)
			if id == "" {
				continue
			}
			upsert(store.ProbeTarget{ID: id, Name: pn.Province + " " + carrierCN[carrier], Carrier: carrier, Province: pn.Province, Host: host, Port: port, Type: "tcp", Source: "zstatic"})
		}
	}
	for _, cn := range z.CityNodes {
		id, host, port := zstaticID(cn.Endpoint)
		if id == "" {
			continue
		}
		upsert(store.ProbeTarget{ID: id, Name: cn.City + " " + carrierCN[cn.Carrier], Carrier: cn.Carrier, Province: cn.Province, City: cn.City, Host: host, Port: port, Type: "tcp", Source: "zstatic"})
	}
	p.St.Set("zstatic_synced_at", time.Now().Format(time.RFC3339)+" ("+src+", 更新于 "+z.UpdatedAt+")")
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

// ---------- push targets to agents ----------

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
	// Segment: entry -> each landing it chains to (SS port, TCP).
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

// ---------- ingest & live state ----------

type probeState struct {
	mu        sync.Mutex
	latest    map[uint]map[string]proto.ProbeSample // server -> target -> last sample
	latestTS  map[uint]map[string]int64
	failRound map[uint]int   // consecutive rounds with every backhaul target lost
	lastOK    map[uint]int64 // last time any backhaul target answered
}

var probes = probeState{
	latest: map[uint]map[string]proto.ProbeSample{}, latestTS: map[uint]map[string]int64{},
	failRound: map[uint]int{}, lastOK: map[uint]int64{},
}

func (p *Panel) ingestProbe(serverID uint, r *proto.ProbeResult) {
	// Agents report loss as 0..1; the panel stores and alerts on percent.
	for i := range r.Samples {
		r.Samples[i].Loss = math.Round(r.Samples[i].Loss*1000) / 10
	}
	rows := make([]store.ProbeResult, 0, len(r.Samples))
	for _, s := range r.Samples {
		rows = append(rows, store.ProbeResult{ServerID: serverID, Target: s.Target, TS: r.TS, Kind: r.Kind, Avg: s.Avg, Min: s.Min, Max: s.Max, Jitter: s.Jitter, Loss: s.Loss})
	}
	if len(rows) > 0 {
		p.St.DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 200)
	}
	probes.mu.Lock()
	if probes.latest[serverID] == nil {
		probes.latest[serverID] = map[string]proto.ProbeSample{}
		probes.latestTS[serverID] = map[string]int64{}
	}
	for _, s := range r.Samples {
		probes.latest[serverID][s.Target] = s
		probes.latestTS[serverID][s.Target] = r.TS
	}
	probes.mu.Unlock()

	switch r.Kind {
	case "backhaul":
		p.evalBackhaul(serverID, r)
	case "segment":
		p.evalSegments(serverID, r)
	}
	p.Bus.Publish("probe", map[string]any{"server_id": serverID, "kind": r.Kind, "ts": r.TS})
}

func (p *Panel) evalBackhaul(serverID uint, r *proto.ProbeResult) {
	if len(r.Samples) == 0 {
		return
	}
	var sumAvg, sumLoss float64
	okN := 0
	for _, s := range r.Samples {
		sumLoss += s.Loss
		if s.Loss < 100 {
			okN++
			sumAvg += s.Avg
		}
	}
	loss := sumLoss / float64(len(r.Samples))
	p.Alert.Observe("backhaul_loss", serverID, loss)
	if okN > 0 {
		p.Alert.Observe("backhaul_latency", serverID, sumAvg/float64(okN))
	}

	probes.mu.Lock()
	if okN > 0 {
		probes.failRound[serverID] = 0
		probes.lastOK[serverID] = r.TS
	} else {
		probes.failRound[serverID]++
	}
	rounds := probes.failRound[serverID]
	// Others reached the same kind of targets recently: the targets are fine.
	othersOK := false
	for id, ts := range probes.lastOK {
		if id != serverID && r.TS-ts < 600 {
			othersOK = true
		}
	}
	probes.mu.Unlock()

	var sv store.Server
	if p.St.DB.Select("id, name, blocked").First(&sv, serverID).Error != nil {
		return
	}
	need := p.St.GetInt("block_rounds", 3)
	switch {
	case okN == 0 && rounds >= need && othersOK && !sv.Blocked:
		p.St.DB.Model(&sv).Update("blocked", true)
		p.Alert.Fire("blocked", serverID, "blocked", "critical",
			fmt.Sprintf("%s 疑似 IP 被封：连续 %d 轮国内三网目标全部不通，其他机器正常", sv.Name, rounds))
		p.Bus.Publish("server", map[string]any{"id": serverID, "blocked": true})
	case okN > 0 && sv.Blocked:
		p.St.DB.Model(&sv).Update("blocked", false)
		p.Alert.Resolve("blocked", serverID, "blocked")
		p.Bus.Publish("server", map[string]any{"id": serverID, "blocked": false})
	}
}

func (p *Panel) evalSegments(entryID uint, r *proto.ProbeResult) {
	for _, s := range r.Samples {
		lid, err := strconv.Atoi(strings.TrimPrefix(s.Target, "seg-"))
		if err != nil {
			continue
		}
		key := "segment-" + s.Target
		if s.Loss >= 100 {
			p.Alert.Fire("segment_down", entryID, key, "warning",
				fmt.Sprintf("%s → %s 线路段中断（落地 SS 端口不通）", p.ServerName(entryID), p.ServerName(uint(lid))))
		} else {
			p.Alert.Resolve("segment_down", entryID, key)
		}
		p.evalFailover(entryID, uint(lid), s.Loss >= 100)
	}
}

func (p *Panel) sample(serverID uint, target string, maxAge int64) (proto.ProbeSample, bool) {
	probes.mu.Lock()
	defer probes.mu.Unlock()
	s, ok := probes.latest[serverID][target]
	if !ok || time.Now().Unix()-probes.latestTS[serverID][target] > maxAge {
		return s, false
	}
	return s, true
}

// backhaulAvg is the mean latency over answering backhaul targets.
func (p *Panel) backhaulAvg(serverID uint) float64 {
	probes.mu.Lock()
	defer probes.mu.Unlock()
	var sum float64
	n := 0
	now := time.Now().Unix()
	for t, s := range probes.latest[serverID] {
		if targetKind(t) == "backhaul" {
			if s.Loss < 100 && now-probes.latestTS[serverID][t] < 600 {
				sum += s.Avg
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return float64(int(sum/float64(n)*10)) / 10
}

func (p *Panel) segmentAvg(entry, landing uint) float64 {
	if s, ok := p.sample(entry, fmt.Sprintf("seg-%d", landing), 600); ok && s.Loss < 100 {
		return s.Avg
	}
	return 0
}

func (p *Panel) segmentDown(entry, landing uint) bool {
	s, ok := p.sample(entry, fmt.Sprintf("seg-%d", landing), 600)
	return ok && s.Loss >= 100
}

// ---------- rollup & queries ----------

// RollupProbes aggregates complete hours into probe_results_1h and trims raw
// data (default 3 days raw, 90 days hourly).
func (p *Panel) RollupProbes() {
	now := time.Now().Unix()
	from := (now - 3*3600) / 3600 * 3600
	to := now / 3600 * 3600
	p.St.DB.Exec(`INSERT OR REPLACE INTO probe_results_1h (server_id, target, ts, kind, avg, min, max, jitter, loss)
		SELECT server_id, target, (ts/3600)*3600, kind,
		       COALESCE(AVG(CASE WHEN loss < 100 THEN avg END), 0), MIN(CASE WHEN loss < 100 THEN min END), MAX(max), AVG(jitter), AVG(loss)
		FROM probe_results WHERE ts >= ? AND ts < ? GROUP BY server_id, target, (ts/3600)`, from, to)
	p.St.DB.Exec("DELETE FROM probe_results WHERE ts < ?", now-int64(p.St.GetInt("retention_probe_raw_days", 3))*86400)
	p.St.DB.Exec("DELETE FROM probe_results_1h WHERE ts < ?", now-int64(p.St.GetInt("retention_probe_1h_days", 90))*86400)
}

// ProbeSeries returns raw rows for ranges up to 2 days, hourly beyond.
func (p *Panel) ProbeSeries(serverID uint, target string, rangeSec int64) []store.ProbeResult {
	since := time.Now().Unix() - rangeSec
	var rows []store.ProbeResult
	if rangeSec <= 2*86400 {
		p.St.DB.Where("server_id = ? AND target = ? AND ts >= ?", serverID, target, since).Order("ts").Find(&rows)
		return rows
	}
	var hs []store.ProbeResultHour
	p.St.DB.Where("server_id = ? AND target = ? AND ts >= ?", serverID, target, since).Order("ts").Find(&hs)
	rows = make([]store.ProbeResult, len(hs))
	for i, h := range hs {
		rows[i] = h.ProbeResult
	}
	return rows
}

type ProbeCell struct {
	ServerID uint    `json:"server_id"`
	Target   string  `json:"target"`
	Avg      float64 `json:"avg"`
	Loss     float64 `json:"loss"`
	Jitter   float64 `json:"jitter"`
	TS       int64   `json:"ts"`
}

// ProbeMatrix returns the latest sample of every server × target of a kind.
func (p *Panel) ProbeMatrix(kind string) []ProbeCell {
	probes.mu.Lock()
	defer probes.mu.Unlock()
	var out []ProbeCell
	for sid, m := range probes.latest {
		for t, s := range m {
			k := targetKind(t)
			if kind != "" && k != kind {
				continue
			}
			out = append(out, ProbeCell{sid, t, s.Avg, s.Loss, s.Jitter, probes.latestTS[sid][t]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerID != out[j].ServerID {
			return out[i].ServerID < out[j].ServerID
		}
		return out[i].Target < out[j].Target
	})
	return out
}

func targetKind(t string) string {
	switch {
	case strings.HasPrefix(t, "seg-"):
		return "segment"
	case strings.HasPrefix(t, "peer-"):
		return "peer"
	case strings.HasPrefix(t, "eg-"):
		return "egress"
	}
	return "backhaul"
}

// LoadProbeState warms the in-memory latest map from the last 10 minutes.
func (p *Panel) LoadProbeState() {
	var rows []store.ProbeResult
	p.St.DB.Where("ts > ?", time.Now().Unix()-600).Order("ts").Find(&rows)
	probes.mu.Lock()
	defer probes.mu.Unlock()
	for _, r := range rows {
		if probes.latest[r.ServerID] == nil {
			probes.latest[r.ServerID] = map[string]proto.ProbeSample{}
			probes.latestTS[r.ServerID] = map[string]int64{}
		}
		probes.latest[r.ServerID][r.Target] = proto.ProbeSample{Target: r.Target, Avg: r.Avg, Min: r.Min, Max: r.Max, Jitter: r.Jitter, Loss: r.Loss}
		probes.latestTS[r.ServerID][r.Target] = r.TS
		if r.Kind == "backhaul" && r.Loss < 100 {
			probes.lastOK[r.ServerID] = r.TS
		}
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

// evalFailover: when the primary landing of a route with backups is
// unreachable from its entry, the leastPing balancer carries the traffic on
// a backup; tell the user once and again when it switches back.
func (p *Panel) evalFailover(entryID, landingID uint, down bool) {
	var rs []store.Route
	p.St.DB.Where("entry_server_id = ? AND landing_server_id = ? AND type = ? AND enabled = ? AND backup_ids <> ''", entryID, landingID, "chain", true).Find(&rs)
	for _, r := range rs {
		key := fmt.Sprintf("failover-r%d", r.ID)
		if down {
			var names []string
			for _, b := range parseIDs(r.BackupIDs) {
				if !p.segmentDown(entryID, b) {
					names = append(names, p.ServerName(b))
				}
			}
			text := fmt.Sprintf("%s → %s 主落地不通", p.ServerName(entryID), p.ServerName(landingID))
			if len(names) > 0 {
				text += "，已切换到备用落地 " + strings.Join(names, "/")
			} else {
				text += "，备用落地也不可用"
			}
			p.Alert.Fire("failover", entryID, key, "warning", text)
		} else {
			p.Alert.Resolve("failover", entryID, key)
		}
	}
}
