package service

import (
	"time"

	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/store"
)

// StartJobs registers the core periodic jobs.
func (p *Panel) StartJobs(s *scheduler.Scheduler) {
	s.Every("telemetry-flush", 2*time.Second, false, p.FlushTelemetry)
	s.Every("online", 10*time.Second, false, p.CheckOnline)
	s.Every("downsample", 5*time.Minute, true, p.Downsample)
	s.Every("tasks-expire", time.Minute, false, p.ExpireTasks)
	s.Every("expiry", time.Hour, true, p.CheckExpiry)
	s.Every("cleanup", 6*time.Hour, true, p.Cleanup)
	s.Every("probe-rollup", time.Hour, false, p.RollupProbes)
	s.Every("traffic-spike", time.Hour, false, p.CheckTrafficSpike)
	s.Daily("zstatic-sync", func() string { return "04:20" }, func() { _, _ = p.SyncZstatic() })

	s.Every("schedules", time.Minute, false, p.RunSchedules)
	s.Daily("backup", func() string { return p.St.Get("backup_time", "03:30") }, p.DailyBackup)
	s.Every("releases", 12*time.Hour, true, func() {
		_ = p.SyncXrayReleases()
		p.CheckAgentVersions()
		p.PruneRelayCache()
	})

	p.SeedScripts()
	p.LoadProbeState()
	var n int64
	p.St.R().Model(&store.ProbeTarget{}).Count(&n)
	if n == 0 {
		go func() { _, _ = p.SyncZstatic() }()
	}
}

// Cleanup trims old logs, telemetry, tool results, and finished tasks.
func (p *Panel) Cleanup() {
	db := p.St.DB
	now := time.Now()
	trafficDays := p.St.GetInt("retention_traffic_days", 400)
	if trafficDays < 1 {
		trafficDays = 400
	}
	toolDays := p.St.GetInt("retention_tool_results_days", 90)
	if toolDays < 1 {
		toolDays = 90
	}
	db.Exec("DELETE FROM sub_access_logs WHERE ts < ?", now.AddDate(0, 0, -90))
	db.Exec("DELETE FROM audit_logs WHERE ts < ?", now.AddDate(0, 0, -365))
	db.Exec("DELETE FROM alert_events WHERE status <> 'firing' AND started_at < ?", now.AddDate(0, 0, -365))
	db.Exec("DELETE FROM tasks WHERE finished_at IS NOT NULL AND finished_at < ?", now.AddDate(0, 0, -60))
	db.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
	db.Exec("DELETE FROM traffic_hourly WHERE hour < ?", now.AddDate(0, 0, -trafficDays).Unix())
	db.Exec("DELETE FROM tool_results WHERE ts < ?", now.AddDate(0, 0, -toolDays))
	db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
}
