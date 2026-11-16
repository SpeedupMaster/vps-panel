package service

import (
	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// Temporary no-ops; each is replaced as its module lands.

func (p *Panel) ingestProbe(serverID uint, r *proto.ProbeResult)     {}
func (p *Panel) onXrayState(serverID uint, s *proto.XrayState)       {}
func (p *Panel) onIPChanged(sv *store.Server, oldIP, newIP string)   {}
func (p *Panel) PushProbeTasks(serverID uint)                        {}
func (p *Panel) afterTask(t *store.Task, r *proto.TaskResult)        {}
func (p *Panel) checkResourceAlerts(serverID uint, m *proto.Metrics) {}
