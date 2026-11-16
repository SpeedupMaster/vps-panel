package service

import (
	"time"

	"vpspanel/internal/panel/scheduler"
)

// StartJobs registers the core periodic jobs.
func (p *Panel) StartJobs(s *scheduler.Scheduler) {
	s.Every("online", 10*time.Second, false, p.CheckOnline)
	s.Every("downsample", 5*time.Minute, true, p.Downsample)
	s.Every("tasks-expire", time.Minute, false, p.ExpireTasks)
	s.Every("expiry", time.Hour, true, p.CheckExpiry)
	s.Every("cleanup", 6*time.Hour, true, p.Cleanup)
}

// Cleanup trims old logs and finished tasks.
func (p *Panel) Cleanup() {
	db := p.St.DB
	now := time.Now()
	db.Exec("DELETE FROM sub_access_logs WHERE ts < ?", now.AddDate(0, 0, -90))
	db.Exec("DELETE FROM audit_logs WHERE ts < ?", now.AddDate(0, 0, -365))
	db.Exec("DELETE FROM alert_events WHERE status <> 'firing' AND started_at < ?", now.AddDate(0, 0, -365))
	db.Exec("DELETE FROM tasks WHERE finished_at IS NOT NULL AND finished_at < ?", now.AddDate(0, 0, -60))
	db.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
}
