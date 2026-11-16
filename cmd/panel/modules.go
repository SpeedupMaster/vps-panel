package main

import (
	"context"

	"vpspanel/internal/panel/api"
	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/service"
)

// setup wires optional feature modules (alerts, probes, subscriptions, ...)
// and returns their scheduler hooks.
func setup(ctx context.Context, p *service.Panel, a *api.API) []func(*scheduler.Scheduler) {
	return nil
}
