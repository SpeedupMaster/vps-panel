package service

import (
	"testing"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestUnwatchAndExpireCleanWaitersMap(t *testing.T) {
	p := newTestPanel(t)
	ch := p.WatchTask(42)
	p.UnwatchTask(42, ch)
	p.mu.RLock()
	_, exists := p.waiters[42]
	p.mu.RUnlock()
	if exists {
		t.Fatal("UnwatchTask left empty key in waiters map")
	}

	// ExpireTasks should also release waiters for timed-out tasks.
	sv := addNode(t, p, "Exp-1", "HK", "1.1.1.1", true, false)
	old := time.Now().Add(-10 * time.Minute)
	task := &store.Task{ID: 999, ServerID: sv.ID, Type: "diag", Status: "running", Timeout: 10, CreatedAt: old}
	if err := p.St.DB.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	wch := p.WatchTask(task.ID)
	p.ExpireTasks()
	select {
	case _, ok := <-wch:
		if ok {
			t.Fatal("expected waiter channel to close on expire")
		}
	default:
		t.Fatal("waiter channel was not closed on ExpireTasks")
	}
	p.mu.RLock()
	_, stillThere := p.waiters[task.ID]
	p.mu.RUnlock()
	if stillThere {
		t.Fatal("ExpireTasks left timed-out task in waiters map")
	}
}

func TestDeleteServerClearsMemoryState(t *testing.T) {
	p := newTestPanel(t)
	e := NewAlertEngine(p)
	p.Alert = e
	sv := addNode(t, p, "Del-1", "HK", "1.1.1.1", true, false)

	p.ingestMetrics(sv.ID, &proto.Metrics{TS: time.Now().Unix(), CPU: 95})
	p.ingestProbe(sv.ID, &proto.ProbeResult{
		TS: time.Now().Unix(), Kind: "backhaul",
		Samples: []proto.ProbeSample{{Target: "zs-bj-ct", Avg: 40, Loss: 0}},
	})
	e.Observe("cpu", sv.ID, 99)

	if err := p.DeleteServer(sv.ID); err != nil {
		t.Fatal(err)
	}
	if p.Latest(sv.ID) != nil {
		t.Fatal("Panel.latest still contains deleted server")
	}
	p.probes.mu.Lock()
	_, hasLatest := p.probes.latest[sv.ID]
	_, hasTS := p.probes.latestTS[sv.ID]
	_, hasFail := p.probes.failRound[sv.ID]
	_, hasOK := p.probes.lastOK[sv.ID]
	p.probes.mu.Unlock()
	if hasLatest || hasTS || hasFail || hasOK {
		t.Fatalf("probes state leaked after DeleteServer: latest=%v ts=%v fail=%v ok=%v", hasLatest, hasTS, hasFail, hasOK)
	}
	e.mu.Lock()
	nSince := len(e.since)
	e.mu.Unlock()
	if nSince != 0 {
		t.Fatalf("AlertEngine.since retained %d entries after DeleteServer", nSince)
	}
}
