package main

import (
	"context"

	"vpspanel/internal/panel/api"
	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/service"
)

// setup wires feature modules that need their own goroutines and returns
// their scheduler hooks.
func setup(ctx context.Context, p *service.Panel, a *api.API) []func(*scheduler.Scheduler) {
	eng := service.NewAlertEngine(p)
	p.Alert = eng
	go p.RunTelegramBot(ctx)
	return []func(*scheduler.Scheduler){eng.Start}
}
