package service

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func (p *Panel) ingestMetrics(serverID uint, m *proto.Metrics) {
	p.mu.Lock()
	p.latest[serverID] = m
	p.mu.Unlock()

	row := store.MetricRaw{Metric: store.Metric{
		ServerID: serverID, TS: m.TS, CPU: m.CPU, Steal: m.Steal, Load1: m.Load1,
		MemUsed: m.MemUsed, SwapUsed: m.SwapUsed, DiskUsed: m.DiskUsed, DiskRead: m.DiskRead, DiskWrite: m.DiskWrite,
		NetIn: m.NetIn, NetOut: m.NetOut, TCP: m.TCPConns, UDP: m.UDPConns,
	}}
	now := time.Now()
	p.teleMu.Lock()
	p.pendingMet = append(p.pendingMet, row)
	if p.pendingSeen == nil {
		p.pendingSeen = map[uint]serverSeenUpdate{}
	}
	u := p.pendingSeen[serverID]
	u.lastSeen = now
	if m.Xray != nil {
		u.hasXray = true
		u.xrayRunning = m.Xray.Running
		if m.Xray.Version != "" {
			u.xrayVersion = m.Xray.Version
		}
	}
	p.pendingSeen[serverID] = u
	shouldFlush := len(p.pendingMet) >= 500
	p.teleMu.Unlock()
	if shouldFlush {
		p.FlushTelemetry()
	}

	p.Bus.Publish("metrics", map[string]any{"id": serverID, "m": m})
	p.checkResourceAlerts(serverID, m)
}

// FlushTelemetry writes all buffered metrics, server heartbeat updates,
// probe results, and dirty streaming task outputs to SQLite in batched
// transactions.
func (p *Panel) FlushTelemetry() {
	p.teleMu.Lock()
	mets := p.pendingMet
	p.pendingMet = nil
	seen := p.pendingSeen
	p.pendingSeen = map[uint]serverSeenUpdate{}
	prs := p.pendingProbes
	p.pendingProbes = nil
	p.teleMu.Unlock()

	if len(mets) > 0 || len(seen) > 0 || len(prs) > 0 {
		_ = p.St.DB.Transaction(func(tx *gorm.DB) error {
			if len(mets) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(mets, 200).Error; err != nil {
					return err
				}
			}
			for sid, u := range seen {
				upd := map[string]any{"last_seen_at": u.lastSeen}
				if p.Hub == nil || p.Hub.Online(sid) {
					upd["status"] = "online"
				}
				if u.hasXray {
					upd["xray_running"] = u.xrayRunning
					if u.xrayVersion != "" {
						upd["xray_version"] = u.xrayVersion
					}
				}
				if err := tx.Model(&store.Server{}).Where("id = ?", sid).Updates(upd).Error; err != nil {
					return err
				}
			}
			if len(prs) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(prs, 200).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}
	p.FlushTaskOutputs()
}

func (p *Panel) dropPendingTelemetry(serverID uint) {
	p.teleMu.Lock()
	delete(p.pendingSeen, serverID)
	if len(p.pendingMet) > 0 {
		kept := p.pendingMet[:0]
		for _, m := range p.pendingMet {
			if m.ServerID != serverID {
				kept = append(kept, m)
			}
		}
		p.pendingMet = kept
	}
	if len(p.pendingProbes) > 0 {
		kept := p.pendingProbes[:0]
		for _, r := range p.pendingProbes {
			if r.ServerID != serverID {
				kept = append(kept, r)
			}
		}
		p.pendingProbes = kept
	}
	p.teleMu.Unlock()
}

const aggCols = `server_id, %s, avg(cpu), avg(steal), avg(load1), avg(mem_used), avg(swap_used), max(disk_used),
	avg(disk_read), avg(disk_write), avg(net_in), avg(net_out), avg(tcp), avg(udp)`

const insCols = `(server_id, ts, cpu, steal, load1, mem_used, swap_used, disk_used, disk_read, disk_write, net_in, net_out, tcp, udp)`

// Downsample rolls raw -> 1m -> 1h -> 1d and enforces retention:
// raw 24h, 1m 7d, 1h 90d (configurable), 1d forever.
func (p *Panel) Downsample() {
	p.FlushTelemetry()
	now := time.Now().Unix()
	db := p.St.DB
	roll := func(src, dst string, step int64, lookback int64) {
		from := (now - lookback) / step * step
		to := now / step * step // only complete buckets
		bucket := fmt.Sprintf("(ts/%d)*%d", step, step)
		q := fmt.Sprintf("INSERT OR REPLACE INTO %s %s SELECT "+aggCols+" FROM %s WHERE ts >= ? AND ts < ? GROUP BY server_id, %s",
			dst, insCols, bucket, src, bucket)
		db.Exec(q, from, to)
	}
	roll("metrics_raw", "metrics_1m", 60, 3600)
	roll("metrics_1m", "metrics_1h", 3600, 3*3600)
	roll("metrics_1h", "metrics_1d", 86400, 2*86400)

	rawDays := 1
	minDays := p.St.GetInt("retention_metrics_1m_days", 7)
	hourDays := p.St.GetInt("retention_metrics_1h_days", 90)
	db.Exec("DELETE FROM metrics_raw WHERE ts < ?", now-int64(rawDays)*86400)
	db.Exec("DELETE FROM metrics_1m WHERE ts < ?", now-int64(minDays)*86400)
	db.Exec("DELETE FROM metrics_1h WHERE ts < ?", now-int64(hourDays)*86400)
	p.St.ShrinkMemory()
}

// MetricSeries picks a resolution that fits the range.
func (p *Panel) MetricSeries(serverID uint, rangeSec int64) []store.Metric {
	p.FlushTelemetry()
	table := "metrics_raw"
	switch {
	case rangeSec > 30*86400:
		table = "metrics_1d"
	case rangeSec > 2*86400:
		table = "metrics_1h"
	case rangeSec > 6*3600:
		table = "metrics_1m"
	}
	var out []store.Metric
	p.St.R().Table(table).Where("server_id = ? AND ts >= ?", serverID, time.Now().Unix()-rangeSec).Order("ts").Find(&out)
	return out
}

// CheckOnline updates suspect / offline status for silent agents.
func (p *Panel) CheckOnline() {
	p.FlushTelemetry()
	var svs []store.Server
	p.St.R().Where("agent_id <> ''").Find(&svs)
	offAfter := time.Duration(p.St.GetInt("offline_after_sec", 90)) * time.Second
	for _, sv := range svs {
		if p.Hub.Online(sv.ID) && time.Since(sv.LastSeenAt) < 30*time.Second {
			continue
		}
		since := time.Since(sv.LastSeenAt)
		switch {
		case since >= offAfter && sv.Status != "offline":
			p.St.DB.Model(&sv).Update("status", "offline")
			p.Alert.Fire("offline", sv.ID, "offline", "critical", fmt.Sprintf("%s 离线（%d 秒无心跳）", sv.Name, int(since.Seconds())))
			p.Bus.Publish("server", map[string]any{"id": sv.ID, "status": "offline"})
		case since >= 30*time.Second && since < offAfter && sv.Status == "online":
			p.St.DB.Model(&sv).Update("status", "suspect")
			p.Bus.Publish("server", map[string]any{"id": sv.ID, "status": "suspect"})
		}
	}
}
