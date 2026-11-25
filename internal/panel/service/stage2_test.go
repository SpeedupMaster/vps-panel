package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestBatchedTelemetryFlushOnReadAndDeleteDrop(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-Batch", "HK", "1.1.1.1", true, false)
	now := time.Now().Unix()

	p.ingestMetrics(sv.ID, &proto.Metrics{TS: now, CPU: 42.5, Xray: &proto.XrayProc{Running: true, Version: "26.1.1"}})
	p.ingestProbe(sv.ID, &proto.ProbeResult{
		TS: now, Kind: "backhaul",
		Samples: []proto.ProbeSample{{Target: "zs-bj-ct", Avg: 35, Loss: 0}},
	})

	// Before flush, rows are buffered in memory (zero immediate DB writes).
	var rawCnt int64
	p.St.R().Model(&store.MetricRaw{}).Where("server_id = ?", sv.ID).Count(&rawCnt)
	if rawCnt != 0 {
		t.Fatalf("expected buffered metrics before flush, got %d in DB", rawCnt)
	}

	// MetricSeries & ProbeSeries auto-flush synchronously so reads are always fresh.
	ms := p.MetricSeries(sv.ID, 3600)
	if len(ms) != 1 || ms[0].CPU != 42.5 {
		t.Fatalf("MetricSeries after auto-flush = %+v", ms)
	}
	ps := p.ProbeSeries(sv.ID, "zs-bj-ct", 3600)
	if len(ps) != 1 || ps[0].Avg != 35 {
		t.Fatalf("ProbeSeries after auto-flush = %+v", ps)
	}

	// Buffered rows for a deleted server are dropped so FlushTelemetry won't resurrect them.
	p.ingestMetrics(sv.ID, &proto.Metrics{TS: now + 10, CPU: 99})
	if err := p.DeleteServer(sv.ID); err != nil {
		t.Fatal(err)
	}
	p.FlushTelemetry()
	p.St.R().Model(&store.MetricRaw{}).Where("server_id = ?", sv.ID).Count(&rawCnt)
	if rawCnt != 0 {
		t.Fatalf("deleted server metrics resurrected on flush: %d", rawCnt)
	}
}

func TestTaskOutputThrottledWritesAndImmediateBroadcast(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-Task", "HK", "1.1.1.1", true, false)
	task, err := p.CreateTask(sv.ID, proto.TaskDiagnose, nil, nil, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	busCh := p.Bus.Subscribe()
	defer p.Bus.Unsubscribe(busCh)

	// Chunk 1 writes immediately; chunks 2 and 3 within 1s are throttled in SQLite
	// while still immediately visible via TaskOutput() and Bus.Publish!
	p.onTaskResult(sv.ID, &proto.TaskResult{TaskID: task.ID, Status: "running", Output: "line1\n"})
	p.onTaskResult(sv.ID, &proto.TaskResult{TaskID: task.ID, Status: "running", Output: "line2\n"})
	p.onTaskResult(sv.ID, &proto.TaskResult{TaskID: task.ID, Status: "running", Output: "line3\n"})

	var mid store.Task
	p.St.R().First(&mid, task.ID)
	if mid.Output != "line1\n" {
		t.Fatalf("intermediate SQLite write was not throttled: got %q, want %q", mid.Output, "line1\n")
	}
	if live := p.TaskOutput(task.ID, mid.Output); live != "line1\nline2\nline3\n" {
		t.Fatalf("TaskOutput buffered = %q, want all 3 lines", live)
	}

	// Verify chunks were broadcast on Bus immediately.
	gotEvents := 0
	for i := 0; i < 3; i++ {
		select {
		case ev := <-busCh:
			if ev.Type == "task_output" {
				gotEvents++
			}
		case <-time.After(time.Second):
		}
	}
	if gotEvents != 3 {
		t.Fatalf("bus task_output broadcasts = %d, want 3", gotEvents)
	}

	// Finished=true flushes all buffered chunks immediately and clears memory entry.
	p.onTaskResult(sv.ID, &proto.TaskResult{TaskID: task.ID, Status: "success", Output: "done\n", Finished: true})
	var done store.Task
	p.St.R().First(&done, task.ID)
	if done.Output != "line1\nline2\nline3\ndone\n" || done.FinishedAt == nil {
		t.Fatalf("final task state = %q finished=%v", done.Output, done.FinishedAt)
	}
	p.taskOutMu.Lock()
	_, stillBuffered := p.taskOut[task.ID]
	p.taskOutMu.Unlock()
	if stillBuffered {
		t.Fatal("taskOut entry leaked after finish")
	}
}

func TestCleanupRetentionTrafficAndToolResults(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-Ret", "HK", "1.1.1.1", true, false)
	_ = p.St.Set("retention_traffic_days", "30")
	_ = p.St.Set("retention_tool_results_days", "15")

	now := time.Now()
	p.St.DB.Create(&store.TrafficHourly{Scope: "server", ScopeID: strconv.FormatUint(uint64(sv.ID), 10), Hour: now.AddDate(0, 0, -45).Unix() / 3600 * 3600, Up: 1})
	p.St.DB.Create(&store.TrafficHourly{Scope: "server", ScopeID: strconv.FormatUint(uint64(sv.ID), 10), Hour: now.AddDate(0, 0, -5).Unix() / 3600 * 3600, Up: 2})
	p.St.DB.Create(&store.ToolResult{ServerID: sv.ID, Tool: "diagnose", Summary: "old", Data: "{}", TS: now.AddDate(0, 0, -20)})
	p.St.DB.Create(&store.ToolResult{ServerID: sv.ID, Tool: "diagnose", Summary: "new", Data: "{}", TS: now.AddDate(0, 0, -2)})

	p.Cleanup()

	var tCnt, trCnt int64
	p.St.R().Model(&store.TrafficHourly{}).Count(&tCnt)
	p.St.R().Model(&store.ToolResult{}).Count(&trCnt)
	if tCnt != 1 || trCnt != 1 {
		t.Fatalf("after Cleanup: traffic_hourly=%d tool_results=%d, want 1 and 1", tCnt, trCnt)
	}
}

func TestServerNameCacheRolesMapAndCycleUsageMap(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-Orig", "HK", "1.1.1.1", true, false)
	jp := addNode(t, p, "JP-Orig", "JP", "2.2.2.2", false, true)
	if _, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Name: "c1"}); err != nil {
		t.Fatal(err)
	}

	if got := p.ServerName(hk.ID); got != "HK-Orig" {
		t.Fatalf("ServerName = %q, want HK-Orig", got)
	}
	if err := p.UpdateServer(hk.ID, ServerInput{Name: "HK-Renamed", Mode: "node", CapReality: true}); err != nil {
		t.Fatal(err)
	}
	if got := p.ServerName(hk.ID); got != "HK-Renamed" {
		t.Fatalf("ServerName after update = %q, want HK-Renamed", got)
	}

	hour := time.Now().Unix() / 3600 * 3600
	p.St.DB.Create(&store.TrafficHourly{Scope: "server", ScopeID: strconv.FormatUint(uint64(hk.ID), 10), Hour: hour, Up: 123, Down: 456})

	var svs []store.Server
	p.St.R().Order("id").Find(&svs)
	rolesMap := p.RolesMap(svs)
	usageMap := p.CycleUsageMap(svs)
	for i := range svs {
		sv := &svs[i]
		if strings.Join(rolesMap[sv.ID], ",") != strings.Join(p.Roles(sv), ",") {
			t.Errorf("RolesMap[%d] = %v, want %v", sv.ID, rolesMap[sv.ID], p.Roles(sv))
		}
		u1, u2 := usageMap[sv.ID], p.CycleUsage(sv)
		if u1.Up != u2.Up || u1.Down != u2.Down || u1.Counted != u2.Counted {
			t.Errorf("CycleUsageMap[%d] = %+v, want %+v", sv.ID, u1, u2)
		}
	}
}

func TestXrayRelayCacheHitIsLockFreeAndRunIperfUniquePorts(t *testing.T) {
	p := newTestPanel(t)
	p.St.DB.Create(&store.Release{Component: "xray", Version: "26.1.1", Channel: "stable", PublishedAt: time.Now()})
	dir := filepath.Join(p.DataDir, "cache", "xray", "v26.1.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(dir, "Xray-linux-64.zip")
	if err := os.WriteFile(cached, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Lock an unrelated asset key simulating a slow download; cached asset must not block!
	otherLock := p.relayKeyLock("26.1.1/Xray-linux-arm64-v8a.zip")
	otherLock.Lock()
	defer otherLock.Unlock()

	done := make(chan string, 1)
	go func() {
		path, err := p.XrayRelayFile("26.1.1", "Xray-linux-64.zip")
		if err == nil {
			done <- path
		}
	}()
	select {
	case got := <-done:
		if got != cached {
			t.Fatalf("XrayRelayFile = %q, want %q", got, cached)
		}
	case <-time.After(time.Second):
		t.Fatal("cached XrayRelayFile was blocked by another asset download")
	}

	// RunIperf allocates distinct ports across consecutive/concurrent runs.
	a := addNode(t, p, "IPerf-A", "HK", "1.1.1.1", true, false)
	b := addNode(t, p, "IPerf-B", "JP", "2.2.2.2", true, false)
	id1, err := p.RunIperf(a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := p.RunIperf(a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var t1, t2 store.Task
	p.St.R().First(&t1, id1)
	p.St.R().First(&t2, id2)
	if t1.Args == t2.Args {
		t.Fatalf("concurrent RunIperf collided on same port: %s", t1.Args)
	}
}

func TestStage2EdgeCasesFlushStatusExpireOutputAndFinishedGuard(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-Edge", "HK", "1.1.1.1", true, false)

	// 1. FlushTelemetry must NOT overwrite "suspect" back to "online" when agent is offline.
	p.St.DB.Model(sv).Update("status", "suspect")
	p.ingestMetrics(sv.ID, &proto.Metrics{TS: time.Now().Unix(), CPU: 15})
	p.FlushTelemetry()
	var afterSV store.Server
	p.St.R().First(&afterSV, sv.ID)
	if afterSV.Status != "suspect" {
		t.Fatalf("FlushTelemetry overwrote disconnected server status to %q, want suspect", afterSV.Status)
	}

	// 2. ExpireTasks preserves existing persisted output when taskOut has no in-memory entry (post-restart).
	old := time.Now().Add(-10 * time.Minute)
	persisted := &store.Task{
		ID: nextTaskID(), ServerID: sv.ID, Type: proto.TaskDiagnose,
		Status: "running", Output: "previous output line\n", Timeout: 10, CreatedAt: old,
	}
	if err := p.St.DB.Create(persisted).Error; err != nil {
		t.Fatal(err)
	}
	p.ExpireTasks()
	var expired store.Task
	p.St.R().First(&expired, persisted.ID)
	if expired.Status != "timeout" || !strings.HasPrefix(expired.Output, "previous output line\n") || !strings.Contains(expired.Output, "任务超时") {
		t.Fatalf("ExpireTasks lost pre-restart output: status=%q output=%q", expired.Status, expired.Output)
	}

	// 3. FlushTaskOutputs must NOT overwrite a task that already has finished_at set.
	now := time.Now()
	finTask := &store.Task{
		ID: nextTaskID(), ServerID: sv.ID, Type: proto.TaskDiagnose,
		Status: "success", Output: "final complete output", FinishedAt: &now, CreatedAt: now,
	}
	if err := p.St.DB.Create(finTask).Error; err != nil {
		t.Fatal(err)
	}
	p.taskOutMu.Lock()
	p.taskOut[finTask.ID] = &taskOutBuf{serverID: sv.ID, status: "running", buf: "stale chunk", dirty: true}
	p.taskOutMu.Unlock()
	p.FlushTaskOutputs()
	var checked store.Task
	p.St.R().First(&checked, finTask.ID)
	if checked.Status != "success" || checked.Output != "final complete output" {
		t.Fatalf("FlushTaskOutputs overwrote finished task: status=%q output=%q", checked.Status, checked.Output)
	}
}
