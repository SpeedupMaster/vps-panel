package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/store"
)

// ingestTraffic stores byte deltas into hourly buckets. Frames are deduped by
// (agent, seq) so replays after a reconnect are harmless.
func (p *Panel) ingestTraffic(c *hub.Conn, t *proto.TrafficDelta) {
	ingested := false
	err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		var seq store.TrafficSeq
		if err := tx.FirstOrCreate(&seq, store.TrafficSeq{AgentID: c.AgentID}).Error; err != nil {
			return err
		}
		// Allow fresh buffer epoch reset when agent restarts its seq counter at 1
		// after having previously progressed beyond 1.
		freshEpoch := t.Seq == 1 && seq.LastSeq > 1
		if !freshEpoch && t.Seq <= seq.LastSeq && seq.LastSeq-t.Seq < 1<<40 {
			return nil
		}
		hour := t.TS / 3600 * 3600
		sid := strconv.FormatUint(uint64(c.ServerID), 10)
		for _, it := range t.Items {
			scope, id := "", ""
			switch it.Scope {
			case "server":
				scope, id = "server", sid
			case "user":
				if strings.HasPrefix(it.Name, "r-") {
					scope, id = "route", strings.TrimPrefix(it.Name, "r-")
				} else {
					scope, id = "landing_user", sid+":"+it.Name
				}
			case "outbound":
				scope, id = "outbound", sid+":"+it.Name
			case "inbound":
				scope, id = "inbound", sid+":"+it.Name
			default:
				continue
			}
			if it.Up == 0 && it.Down == 0 {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "scope"}, {Name: "scope_id"}, {Name: "hour"}},
				DoUpdates: clause.Assignments(map[string]any{"up": clause.Expr{SQL: "up + ?", Vars: []any{it.Up}}, "down": clause.Expr{SQL: "down + ?", Vars: []any{it.Down}}}),
			}).Create(&store.TrafficHourly{Scope: scope, ScopeID: id, Hour: hour, Up: it.Up, Down: it.Down}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&store.TrafficSeq{}).Where("agent_id = ?", c.AgentID).Update("last_seq", t.Seq).Error; err != nil {
			return err
		}
		ingested = true
		return nil
	})
	if err != nil {
		return
	}
	_ = p.Hub.Send(c.ServerID, &proto.PanelMessage{Ack: &proto.Ack{Seq: t.Seq}})
	if ingested {
		p.checkTrafficQuota(c.ServerID)
	}
}

type Usage struct {
	Up         uint64     `json:"up"`
	Down       uint64     `json:"down"`
	Counted    uint64     `json:"counted"` // per count_mode
	Quota      int64      `json:"quota"`
	Percent    float64    `json:"percent"`
	CycleStart time.Time  `json:"cycle_start"`
	CycleEnd   time.Time  `json:"cycle_end"`
	ForecastAt *time.Time `json:"forecast_at,omitempty"` // when the quota runs out at the current pace
}

// Sum returns up/down for a scope since a time.
func (p *Panel) Sum(scope, scopeID string, since time.Time) (up, down uint64) {
	var r struct{ Up, Down uint64 }
	p.St.R().Model(&store.TrafficHourly{}).Select("COALESCE(SUM(up),0) AS up, COALESCE(SUM(down),0) AS down").
		Where("scope = ? AND scope_id = ? AND hour >= ?", scope, scopeID, since.Unix()/3600*3600).Scan(&r)
	return r.Up, r.Down
}

// SumMany returns up/down totals for multiple scopeIDs in a single GROUP BY query.
func (p *Panel) SumMany(scope string, scopeIDs []string, since time.Time) map[string][2]uint64 {
	out := make(map[string][2]uint64, len(scopeIDs))
	if len(scopeIDs) == 0 {
		return out
	}
	var rows []struct {
		ScopeID string
		Up      uint64
		Down    uint64
	}
	p.St.R().Model(&store.TrafficHourly{}).
		Select("scope_id, COALESCE(SUM(up),0) AS up, COALESCE(SUM(down),0) AS down").
		Where("scope = ? AND scope_id IN ? AND hour >= ?", scope, scopeIDs, since.Unix()/3600*3600).
		Group("scope_id").Scan(&rows)
	for _, r := range rows {
		out[r.ScopeID] = [2]uint64{r.Up, r.Down}
	}
	return out
}

func buildUsage(sv *store.Server, up, down uint64, start, end, now time.Time) Usage {
	u := Usage{Up: up, Down: down, Quota: sv.QuotaBytes, CycleStart: start, CycleEnd: end}
	switch sv.CountMode {
	case "in":
		u.Counted = down
	case "both":
		u.Counted = up + down
	case "max":
		u.Counted = max(up, down)
	default:
		u.Counted = up
	}
	if sv.QuotaBytes > 0 {
		u.Percent = float64(u.Counted) / float64(sv.QuotaBytes) * 100
		elapsed := now.Sub(start).Hours()
		if elapsed > 6 && u.Counted > 0 {
			rate := float64(u.Counted) / elapsed // bytes per hour
			left := float64(sv.QuotaBytes) - float64(u.Counted)
			if left > 0 {
				at := now.Add(time.Duration(left/rate) * time.Hour)
				if at.Before(end) {
					u.ForecastAt = &at
				}
			}
		}
	}
	return u
}

// CycleUsage is the server's NIC traffic in the current billing cycle.
// For the NIC, "up" = bytes sent (out), "down" = bytes received (in).
func (p *Panel) CycleUsage(sv *store.Server) Usage {
	now := time.Now()
	start := CycleStart(sv.ResetDay, now)
	end := clampDay(start.Year(), start.Month()+1, sv.ResetDay, now.Location())
	up, down := p.Sum("server", strconv.FormatUint(uint64(sv.ID), 10), start)
	return buildUsage(sv, up, down, start, end, now)
}

// CycleUsageMap computes CycleUsage for a batch of servers in a single SQL query.
func (p *Panel) CycleUsageMap(svs []store.Server) map[uint]Usage {
	out := make(map[uint]Usage, len(svs))
	if len(svs) == 0 {
		return out
	}
	now := time.Now()
	starts := make(map[uint]time.Time, len(svs))
	ends := make(map[uint]time.Time, len(svs))
	startHours := make(map[string]int64, len(svs))
	sids := make([]string, 0, len(svs))
	var minHour int64
	for i := range svs {
		sv := &svs[i]
		start := CycleStart(sv.ResetDay, now)
		end := clampDay(start.Year(), start.Month()+1, sv.ResetDay, now.Location())
		sh := start.Unix() / 3600 * 3600
		sid := strconv.FormatUint(uint64(sv.ID), 10)
		starts[sv.ID] = start
		ends[sv.ID] = end
		startHours[sid] = sh
		sids = append(sids, sid)
		if i == 0 || sh < minHour {
			minHour = sh
		}
	}
	var rows []store.TrafficHourly
	p.St.R().Select("scope_id, hour, up, down").
		Where("scope = ? AND scope_id IN ? AND hour >= ?", "server", sids, minHour).
		Find(&rows)
	totals := make(map[string][2]uint64, len(svs))
	for _, r := range rows {
		if r.Hour >= startHours[r.ScopeID] {
			cur := totals[r.ScopeID]
			cur[0] += r.Up
			cur[1] += r.Down
			totals[r.ScopeID] = cur
		}
	}
	for i := range svs {
		sv := &svs[i]
		sid := strconv.FormatUint(uint64(sv.ID), 10)
		t := totals[sid]
		out[sv.ID] = buildUsage(sv, t[0], t[1], starts[sv.ID], ends[sv.ID], now)
	}
	return out
}

// checkTrafficQuota fires 80/90/100% alerts once per level per cycle and
// optionally stops Xray when the quota is exceeded.
func (p *Panel) checkTrafficQuota(serverID uint) {
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return
	}
	u := p.CycleUsage(&sv)
	cycle := u.CycleStart.Format("20060102")
	// Auto-resolve firing traffic alerts from prior billing cycles (or when quota removed).
	var active []store.AlertEvent
	p.St.DB.Where("kind = ? AND server_id = ? AND status = ?", "traffic", sv.ID, "firing").Find(&active)
	for _, ev := range active {
		if sv.QuotaBytes <= 0 || !strings.HasSuffix(ev.Key, "-"+cycle) {
			p.Alert.Resolve("traffic", sv.ID, ev.Key)
		}
	}
	if sv.QuotaBytes <= 0 {
		return
	}
	for _, lvl := range []int{100, 90, 80} {
		if u.Percent < float64(lvl) {
			continue
		}
		key := fmt.Sprintf("traffic-%d-%s", lvl, cycle)
		sev := "info"
		if lvl == 100 {
			sev = "critical"
		} else if lvl == 90 {
			sev = "warning"
		}
		text := fmt.Sprintf("%s 本周期流量已用 %.0f%%（%s / %s）", sv.Name, u.Percent, HumanBytes(u.Counted), HumanBytes(uint64(sv.QuotaBytes)))
		if u.ForecastAt != nil && lvl < 100 {
			text += "，预计 " + u.ForecastAt.Format("01-02") + " 用完"
		}
		p.Alert.Fire("traffic", sv.ID, key, sev, text)
		if lvl == 100 && sv.OnExceeded == "stop_xray" && sv.Mode == "node" && sv.XrayRunning {
			p.CreateTask(sv.ID, proto.TaskRestartXray, map[string]string{"action": "stop"}, nil, 60, "")
			p.Audit("system", "stop_xray_quota", sv.Name, "流量超额自动停止 Xray", "")
		}
		break
	}
}

// TrafficSeries returns hourly or daily buckets for a scope.
func (p *Panel) TrafficSeries(scope, scopeID string, since time.Time, daily bool) []store.TrafficHourly {
	var rows []store.TrafficHourly
	q := p.St.R().Model(&store.TrafficHourly{}).Where("scope = ? AND scope_id = ? AND hour >= ?", scope, scopeID, since.Unix())
	if daily {
		// Group by local day.
		_, off := time.Now().Zone()
		q.Select(fmt.Sprintf("scope, scope_id, ((hour + %d) / 86400) * 86400 - %d AS hour, SUM(up) AS up, SUM(down) AS down", off, off)).
			Group(fmt.Sprintf("(hour + %d) / 86400", off)).Order("hour").Scan(&rows)
		return rows
	}
	q.Order("hour").Find(&rows)
	return rows
}

func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// CheckTrafficSpike runs hourly: the last full hour of a server is compared
// with its trailing 24h hourly average (factor traffic_spike_factor, default
// 5x, and at least traffic_spike_min_gb, default 2 GB).
func (p *Panel) CheckTrafficSpike() {
	factor := float64(p.St.GetInt("traffic_spike_factor", 5))
	minBytes := uint64(p.St.GetInt("traffic_spike_min_gb", 2)) << 30
	last := time.Now().Unix()/3600*3600 - 3600
	var svs []store.Server
	p.St.DB.Find(&svs)
	for _, sv := range svs {
		sid := strconv.FormatUint(uint64(sv.ID), 10)
		var rows []store.TrafficHourly
		p.St.DB.Where("scope = ? AND scope_id = ? AND hour >= ? AND hour <= ?", "server", sid, last-24*3600, last).Find(&rows)
		var cur, sum uint64
		n := 0
		for _, r := range rows {
			if r.Hour == last {
				cur = r.Up + r.Down
			} else {
				sum += r.Up + r.Down
				n++
			}
		}
		if n < 6 || cur < minBytes {
			continue
		}
		avg := float64(sum) / float64(n)
		if float64(cur) > avg*factor {
			p.Alert.Info("traffic_spike", sv.ID, fmt.Sprintf("%s 最近一小时流量 %s，是过去 24 小时均值（%s/小时）的 %.1f 倍",
				sv.Name, HumanBytes(cur), HumanBytes(uint64(avg)), float64(cur)/avg))
		}
	}
}
