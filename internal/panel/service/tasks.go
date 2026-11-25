package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

var lastTaskID atomic.Uint64

// nextTaskID returns a strictly increasing task ID based on UnixNano.
func nextTaskID() uint64 {
	now := uint64(time.Now().UnixNano())
	for {
		prev := lastTaskID.Load()
		next := now
		if next <= prev {
			next = prev + 1
		}
		if lastTaskID.CompareAndSwap(prev, next) {
			return next
		}
	}
}

// CreateTask persists a task and sends it right away when the agent is
// online; otherwise it waits in the queue until the agent reconnects.
func (p *Panel) CreateTask(serverID uint, typ string, args map[string]string, payload []byte, timeout int, batch string) (*store.Task, error) {
	if timeout <= 0 {
		timeout = 300
	}
	a, _ := json.Marshal(args)
	exp := time.Now().Add(24 * time.Hour)
	t := &store.Task{
		ID: nextTaskID(), ServerID: serverID, BatchID: batch, Type: typ, Args: string(a), Payload: payload,
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

func trimTaskOutputTail(out string) string {
	const maxBytes = 256 << 10
	if len(out) <= maxBytes {
		return out
	}
	start := len(out) - maxBytes
	for start < len(out) && !utf8.RuneStart(out[start]) {
		start++
	}
	return out[start:]
}

// TaskOutput returns the freshest output for a task (including any buffered
// streaming chunks not yet flushed to SQLite).
func (p *Panel) TaskOutput(id uint64, stored string) string {
	p.taskOutMu.Lock()
	defer p.taskOutMu.Unlock()
	if b := p.taskOut[id]; b != nil {
		return b.buf
	}
	return stored
}

// FlushTaskOutputs flushes any dirty in-memory streaming task output buffers
// to SQLite.
func (p *Panel) FlushTaskOutputs() {
	type item struct {
		id     uint64
		status string
		out    string
	}
	now := time.Now()
	p.taskOutMu.Lock()
	var items []item
	for id, b := range p.taskOut {
		if b.dirty {
			items = append(items, item{id: id, status: b.status, out: b.buf})
			b.dirty = false
			b.lastFlush = now
		}
	}
	p.taskOutMu.Unlock()
	for _, it := range items {
		upd := map[string]any{"output": it.out}
		if it.status != "" {
			upd["status"] = it.status
		}
		p.St.DB.Model(&store.Task{}).Where("id = ? AND finished_at IS NULL", it.id).Updates(upd)
	}
}

func (p *Panel) finishTask(t *store.Task, status, out string) {
	now := time.Now()
	p.taskOutMu.Lock()
	base := t.Output
	hasBuf := false
	if b := p.taskOut[t.ID]; b != nil {
		base = b.buf
		hasBuf = true
		delete(p.taskOut, t.ID)
	}
	p.taskOutMu.Unlock()
	if !hasBuf && base == "" && t.ID != 0 {
		var cur struct{ Output string }
		if p.St.DB.Model(&store.Task{}).Select("output").Where("id = ?", t.ID).First(&cur).Error == nil {
			base = cur.Output
		}
	}
	p.St.DB.Model(t).Updates(map[string]any{"status": status, "output": trimTaskOutputTail(base + out), "finished_at": &now})
	p.mu.Lock()
	ws := p.waiters[t.ID]
	delete(p.waiters, t.ID)
	p.mu.Unlock()
	for _, w := range ws {
		close(w)
	}
}

func (p *Panel) onTaskResult(serverID uint, r *proto.TaskResult) {
	var t store.Task
	p.taskOutMu.Lock()
	existing := p.taskOut[r.TaskID]
	p.taskOutMu.Unlock()

	q := p.St.DB.Where("id = ? AND server_id = ?", r.TaskID, serverID)
	if existing != nil {
		q = q.Omit("output", "payload")
	} else {
		q = q.Omit("payload")
	}
	if err := q.First(&t).Error; err != nil {
		return
	}

	upd := map[string]any{}
	statusChanged := r.Status != "" && r.Status != t.Status
	if statusChanged || r.Finished {
		upd["status"] = r.Status
	}
	now := time.Now()
	if t.Type != proto.TaskShell { // never persist terminal sessions
		p.taskOutMu.Lock()
		if p.taskOut == nil {
			p.taskOut = map[uint64]*taskOutBuf{}
		}
		b := p.taskOut[r.TaskID]
		if b == nil {
			b = &taskOutBuf{serverID: serverID, buf: t.Output}
			p.taskOut[r.TaskID] = b
		}
		if r.Status != "" {
			b.status = r.Status
		}
		if r.Output != "" {
			b.buf = trimTaskOutputTail(b.buf + r.Output)
			b.dirty = true
		}
		if r.Finished {
			upd["output"] = b.buf
			b.dirty = false
			b.lastFlush = now
			delete(p.taskOut, r.TaskID)
		} else if b.dirty && (r.Data != nil || b.lastFlush.IsZero() || now.Sub(b.lastFlush) >= time.Second) {
			upd["output"] = b.buf
			b.dirty = false
			b.lastFlush = now
		}
		p.taskOutMu.Unlock()
	}
	if r.Data != nil {
		b, _ := json.Marshal(r.Data)
		upd["data"] = string(b)
	}
	if r.Finished {
		upd["finished_at"] = &now
	}
	if len(upd) > 0 {
		p.St.DB.Model(&t).Updates(upd)
	}

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
		p.Bus.Publish("task", map[string]any{"id": strconv.FormatUint(r.TaskID, 10), "server_id": serverID, "type": t.Type, "status": r.Status})
		p.afterTask(&t, r)
	} else if r.Output != "" {
		p.Bus.Publish("task_output", map[string]any{"id": strconv.FormatUint(r.TaskID, 10), "server_id": serverID, "output": r.Output})
	}
}

// WatchTask streams results for a task until it finishes.
func (p *Panel) WatchTask(id uint64) chan *proto.TaskResult { return p.WatchTaskN(id, 128) }

// WatchTaskN is WatchTask with a custom buffer (WebSSH output is chatty).
func (p *Panel) WatchTaskN(id uint64, size int) chan *proto.TaskResult {
	ch := make(chan *proto.TaskResult, size)
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
			ws = append(ws[:i], ws[i+1:]...)
			if len(ws) == 0 {
				delete(p.waiters, id)
			} else {
				p.waiters[id] = ws
			}
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
	p.St.DB.Omit("output", "payload").Where("status IN ?", []string{"sent", "running"}).Find(&ts)
	for i := range ts {
		if time.Since(ts[i].CreatedAt) > time.Duration(ts[i].Timeout+60)*time.Second {
			p.finishTask(&ts[i], "timeout", "\n任务超时")
		}
	}
}
