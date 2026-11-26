package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// ---------- ingest & live state ----------

type probeState struct {
	mu        sync.Mutex
	latest    map[uint]map[string]proto.ProbeSample // server -> target -> last sample
	latestTS  map[uint]map[string]int64
	failRound map[uint]int   // consecutive rounds with every backhaul target lost
	lastOK    map[uint]int64 // last time any backhaul target answered
}

func newProbeState() *probeState {
	return &probeState{
		latest:    map[uint]map[string]proto.ProbeSample{},
		latestTS:  map[uint]map[string]int64{},
		failRound: map[uint]int{},
		lastOK:    map[uint]int64{},
	}
}

func (p *Panel) probeStore() *probeState {
	if p.probes == nil {
		p.probes = newProbeState()
	}
	return p.probes
}

func (p *Panel) clearProbeState(serverID uint) {
	ps := p.probeStore()
	ps.mu.Lock()
	delete(ps.latest, serverID)
	delete(ps.latestTS, serverID)
	delete(ps.failRound, serverID)
	delete(ps.lastOK, serverID)
	ps.mu.Unlock()
}

// ForgetProbeTarget drops in-memory samples for a deleted custom probe target.
func (p *Panel) ForgetProbeTarget(target string) {
	ps := p.probeStore()
	ps.mu.Lock()
	for sid := range ps.latest {
		delete(ps.latest[sid], target)
		delete(ps.latestTS[sid], target)
	}
	ps.mu.Unlock()
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
		p.teleMu.Lock()
		p.pendingProbes = append(p.pendingProbes, rows...)
		shouldFlush := len(p.pendingProbes) >= 500
		p.teleMu.Unlock()
		if shouldFlush {
			p.FlushTelemetry()
		}
	}
	ps := p.probeStore()
	ps.mu.Lock()
	if ps.latest[serverID] == nil {
		ps.latest[serverID] = map[string]proto.ProbeSample{}
		ps.latestTS[serverID] = map[string]int64{}
	}
	for _, s := range r.Samples {
		ps.latest[serverID][s.Target] = s
		ps.latestTS[serverID][s.Target] = r.TS
	}
	ps.mu.Unlock()

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

	ps := p.probeStore()
	ps.mu.Lock()
	if okN > 0 {
		ps.failRound[serverID] = 0
		ps.lastOK[serverID] = r.TS
	} else {
		ps.failRound[serverID]++
	}
	rounds := ps.failRound[serverID]
	// Others reached the same kind of targets recently: the targets are fine.
	othersOK := false
	for id, ts := range ps.lastOK {
		if id != serverID && r.TS-ts < 600 {
			othersOK = true
		}
	}
	ps.mu.Unlock()

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
	ps := p.probeStore()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	s, ok := ps.latest[serverID][target]
	if !ok || time.Now().Unix()-ps.latestTS[serverID][target] > maxAge {
		return s, false
	}
	return s, true
}

// backhaulAvg is the mean latency over answering backhaul targets.
func (p *Panel) backhaulAvg(serverID uint) float64 {
	ps := p.probeStore()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var sum float64
	n := 0
	now := time.Now().Unix()
	for t, s := range ps.latest[serverID] {
		if targetKind(t) == "backhaul" {
			if s.Loss < 100 && now-ps.latestTS[serverID][t] < 600 {
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
	p.FlushTelemetry()
	now := time.Now().Unix()
	from := (now - 3*3600) / 3600 * 3600
	to := now / 3600 * 3600
	p.St.DB.Exec(`INSERT OR REPLACE INTO probe_results_1h (server_id, target, ts, kind, avg, min, max, jitter, loss)
		SELECT server_id, target, (ts/3600)*3600, kind,
		       COALESCE(AVG(CASE WHEN loss < 100 THEN avg END), 0), MIN(CASE WHEN loss < 100 THEN min END), MAX(max), AVG(jitter), AVG(loss)
		FROM probe_results WHERE ts >= ? AND ts < ? GROUP BY server_id, target, (ts/3600)`, from, to)
	p.St.DB.Exec("DELETE FROM probe_results WHERE ts < ?", now-int64(p.St.GetInt("retention_probe_raw_days", 3))*86400)
	p.St.DB.Exec("DELETE FROM probe_results_1h WHERE ts < ?", now-int64(p.St.GetInt("retention_probe_1h_days", 90))*86400)
	p.St.ShrinkMemory()
}

// ProbeSeries returns raw rows for ranges up to 2 days, hourly beyond.
func (p *Panel) ProbeSeries(serverID uint, target string, rangeSec int64) []store.ProbeResult {
	return p.ProbeSeriesMany(serverID, []string{target}, rangeSec)[target]
}

// ProbeSeriesMany fetches probe series for multiple targets in a single query.
func (p *Panel) ProbeSeriesMany(serverID uint, targets []string, rangeSec int64) map[string][]store.ProbeResult {
	out := make(map[string][]store.ProbeResult, len(targets))
	uniq := make([]string, 0, len(targets))
	for _, t := range targets {
		if t == "" {
			continue
		}
		if _, seen := out[t]; !seen {
			out[t] = []store.ProbeResult{}
			uniq = append(uniq, t)
		}
	}
	if len(uniq) == 0 {
		return out
	}
	p.FlushTelemetry()
	since := time.Now().Unix() - rangeSec
	if rangeSec <= 2*86400 {
		var rows []store.ProbeResult
		p.St.R().Where("server_id = ? AND target IN ? AND ts >= ?", serverID, uniq, since).Order("ts").Find(&rows)
		for _, r := range rows {
			out[r.Target] = append(out[r.Target], r)
		}
		return out
	}
	var hs []store.ProbeResultHour
	p.St.R().Where("server_id = ? AND target IN ? AND ts >= ?", serverID, uniq, since).Order("ts").Find(&hs)
	for _, h := range hs {
		out[h.Target] = append(out[h.Target], h.ProbeResult)
	}
	return out
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
	ps := p.probeStore()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var out []ProbeCell
	for sid, m := range ps.latest {
		for t, s := range m {
			k := targetKind(t)
			if kind != "" && k != kind {
				continue
			}
			out = append(out, ProbeCell{sid, t, s.Avg, s.Loss, s.Jitter, ps.latestTS[sid][t]})
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
	ps := p.probeStore()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, r := range rows {
		if ps.latest[r.ServerID] == nil {
			ps.latest[r.ServerID] = map[string]proto.ProbeSample{}
			ps.latestTS[r.ServerID] = map[string]int64{}
		}
		ps.latest[r.ServerID][r.Target] = proto.ProbeSample{Target: r.Target, Avg: r.Avg, Min: r.Min, Max: r.Max, Jitter: r.Jitter, Loss: r.Loss}
		ps.latestTS[r.ServerID][r.Target] = r.TS
		if r.Kind == "backhaul" && r.Loss < 100 {
			ps.lastOK[r.ServerID] = r.TS
		}
	}
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
