package service

import (
	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// Temporary no-ops; each is replaced as its module lands.

func (p *Panel) afterToolTask(t *store.Task, r *proto.TaskResult) {}
