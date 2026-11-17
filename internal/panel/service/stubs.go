package service

import (
	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// Temporary no-ops; each is replaced as its module lands.

func (p *Panel) ingestProbe(serverID uint, r *proto.ProbeResult)     {}
func (p *Panel) PushProbeTasks(serverID uint)                        {}
func (p *Panel) afterToolTask(t *store.Task, r *proto.TaskResult)    {}
func (p *Panel) checkResourceAlerts(serverID uint, m *proto.Metrics) {}
func (p *Panel) backhaulAvg(serverID uint) float64                   { return 0 }
func (p *Panel) segmentAvg(entry, landing uint) float64              { return 0 }
func (p *Panel) segmentDown(entry, landing uint) bool                { return false }
