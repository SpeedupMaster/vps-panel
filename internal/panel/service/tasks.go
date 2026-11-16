package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// CreateTask persists a task and sends it right away when the agent is
// online; otherwise it waits in the queue until the agent reconnects.
func (p *Panel) CreateTask(serverID uint, typ string, args map[string]string, payload []byte, timeout int, batch string) (*store.Task, error) {
	if timeout <= 0 {
		timeout = 300
	}
	a, _ := json.Marshal(args)
	exp := time.Now().Add(24 * time.Hour)
	t := &store.Task{
		ID: uint64(time.Now().UnixNano()), ServerID: serverID, BatchID: batch, Type: typ, Args: string(a), Payload: payload,
		Status: "pending", Timeout: timeout, ExpireAt: &exp, CreatedAt: time.Now(),
	}
	if err := p.St.DB.Create(t).Error; err != nil {
		return nil, err
	}
	p.dispatch(t)
	return t, nil
}

func (p *Panel) dispatch(t *store.Task) {
	var args map[string]string
	_ = json.Unmarshal([]byte(t.Args), &args)
	err := p.Hub.Send(t.ServerID, &proto.PanelMessage{Task: &proto.Task{ID: t.ID, Type: t.Type, Args: args, Payload: t.Payload, Timeout: t.Timeout}})
	if err == nil {
		p.St.DB.Model(t).Update("status", "sent")
		t.Status = "sent"
	}
}

func (p *Panel) flushPendingTasks(serverID uint) {
	var ts []store.Task
	p.St.DB.Where("server_id = ? AND status = ?", serverID, "pending").Order("id").Find(&ts)
	for i := range ts {
		if ts[i].ExpireAt != nil && time.Now().After(*ts[i].ExpireAt) {
			p.finishTask(&ts[i], "timeout", "任务在 Agent 离线期间过期")
			continue
		}
		p.dispatch(&ts[i])
	}
}

func (p *Panel) finishTask(t *store.Task, status, out string) {
	now := time.Now()
	p.St.DB.Model(t).Updates(map[string]any{"status": status, "output": t.Output + out, "finished_at": &now})
}

func (p *Panel) onTaskResult(serverID uint, r *proto.TaskResult) {
	var t store.Task
	if err := p.St.DB.First(&t, "id = ? AND server_id = ?", r.TaskID, serverID).Error; err != nil {
		return
	}
	upd := map[string]any{"status": r.Status}
	if r.Output != "" {
		out := t.Output + r.Output
		if len(out) > 256<<10 { // keep the tail
			out = out[len(out)-256<<10:]
		}
		upd["output"] = out
	}
	if r.Data != nil {
		b, _ := json.Marshal(r.Data)
		upd["data"] = string(b)
	}
	if r.Finished {
		now := time.Now()
		upd["finished_at"] = &now
	}
	p.St.DB.Model(&t).Updates(upd)

	p.mu.Lock()
	ws := p.waiters[r.TaskID]
	if r.Finished {
		delete(p.waiters, r.TaskID)
	}
	p.mu.Unlock()
	for _, w := range ws {
		select {
		case w <- r:
		default:
		}
		if r.Finished {
			close(w)
		}
	}
	if r.Finished {
		p.Bus.Publish("task", map[string]any{"id": r.TaskID, "server_id": serverID, "type": t.Type, "status": r.Status})
		p.afterTask(&t, r)
	}
}

// WatchTask streams results for a task until it finishes.
func (p *Panel) WatchTask(id uint64) chan *proto.TaskResult {
	ch := make(chan *proto.TaskResult, 128)
	p.mu.Lock()
	p.waiters[id] = append(p.waiters[id], ch)
	p.mu.Unlock()
	return ch
}

func (p *Panel) UnwatchTask(id uint64, ch chan *proto.TaskResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ws := p.waiters[id]
	for i, w := range ws {
		if w == ch {
			p.waiters[id] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
}

// RunTask creates a task and waits for its final result.
func (p *Panel) RunTask(ctx context.Context, serverID uint, typ string, args map[string]string, payload []byte, timeout int) (*proto.TaskResult, string, error) {
	if !p.Hub.Online(serverID) {
		return nil, "", errors.New("Agent 不在线")
	}
	t, err := p.CreateTask(serverID, typ, args, payload, timeout, "")
	if err != nil {
		return nil, "", err
	}
	ch := p.WatchTask(t.ID)
	defer p.UnwatchTask(t.ID, ch)
	if timeout <= 0 {
		timeout = 300
	}
	tm := time.NewTimer(time.Duration(timeout+10) * time.Second)
	defer tm.Stop()
	var out strings.Builder
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				var final store.Task
				p.St.DB.First(&final, t.ID)
				return &proto.TaskResult{TaskID: t.ID, Status: final.Status, Finished: true}, final.Output, nil
			}
			out.WriteString(r.Output)
			if r.Finished {
				return r, out.String(), nil
			}
		case <-tm.C:
			p.finishTask(t, "timeout", "\n等待结果超时")
			return nil, out.String(), errors.New("任务超时")
		case <-ctx.Done():
			return nil, out.String(), ctx.Err()
		}
	}
}

// ExpireTasks marks sent/running tasks that ran past their timeout.
func (p *Panel) ExpireTasks() {
	var ts []store.Task
	p.St.DB.Where("status IN ?", []string{"sent", "running"}).Find(&ts)
	for i := range ts {
		if time.Since(ts[i].CreatedAt) > time.Duration(ts[i].Timeout+60)*time.Second {
			p.finishTask(&ts[i], "timeout", "\n任务超时")
		}
	}
}
