package service

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/store"
)

func TestIngestTrafficTransactionalAndSeqReset(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-T", "HK", "1.1.1.1", true, false)
	c := &hub.Conn{ServerID: sv.ID, AgentID: sv.AgentID}
	now := time.Now().Unix()
	sid := strconv.FormatUint(uint64(sv.ID), 10)

	// Seq 1: normal ingest across multiple scopes.
	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 1, TS: now,
		Items: []proto.TrafficItem{
			{Scope: "server", Name: "eth0", Up: 100, Down: 200},
			{Scope: "user", Name: "r-7", Up: 10, Down: 20},
			{Scope: "user", Name: "from-2", Up: 5, Down: 6},
			{Scope: "outbound", Name: "direct", Up: 7, Down: 8},
			{Scope: "inbound", Name: "reality-in", Up: 9, Down: 11},
		},
	})
	// Duplicate Seq 1 must NOT double-count.
	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 1, TS: now,
		Items: []proto.TrafficItem{{Scope: "server", Name: "eth0", Up: 100, Down: 200}},
	})
	// Seq 2 accumulates.
	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 2, TS: now,
		Items: []proto.TrafficItem{{Scope: "server", Name: "eth0", Up: 50, Down: 60}},
	})

	since := time.Now().Add(-2 * time.Hour)
	up, down := p.Sum("server", sid, since)
	if up != 150 || down != 260 {
		t.Fatalf("after seq 1,1,2: server up=%d down=%d, want 150/260", up, down)
	}
	rup, rdown := p.Sum("route", "7", since)
	if rup != 10 || rdown != 20 {
		t.Fatalf("route up=%d down=%d, want 10/20", rup, rdown)
	}
	lup, ldown := p.Sum("landing_user", sid+":from-2", since)
	if lup != 5 || ldown != 6 {
		t.Fatalf("landing_user up=%d down=%d, want 5/6", lup, ldown)
	}

	// Fresh buffer epoch: Seq 1 after LastSeq > 1 resets seq cleanly and ingests!
	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 1, TS: now,
		Items: []proto.TrafficItem{{Scope: "server", Name: "eth0", Up: 30, Down: 40}},
	})
	var seq store.TrafficSeq
	p.St.DB.First(&seq, "agent_id = ?", sv.AgentID)
	if seq.LastSeq != 1 {
		t.Fatalf("LastSeq after fresh epoch reset = %d, want 1", seq.LastSeq)
	}
	up, down = p.Sum("server", sid, since)
	if up != 180 || down != 300 {
		t.Fatalf("after epoch reset: up=%d down=%d, want 180/300", up, down)
	}
	// Next frame in new epoch (Seq 2) counts normally.
	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 2, TS: now,
		Items: []proto.TrafficItem{{Scope: "server", Name: "eth0", Up: 20, Down: 10}},
	})
	up, down = p.Sum("server", sid, since)
	if up != 200 || down != 310 {
		t.Fatalf("after seq 2 in new epoch: up=%d down=%d, want 200/310", up, down)
	}

	// Transaction atomicity: force a failure on TrafficSeq update and verify TrafficHourly rolled back.
	const cbName = "test:fail_traffic_seq_update"
	if err := p.St.DB.Callback().Update().Before("gorm:update").Register(cbName, func(db *gorm.DB) {
		if db.Statement != nil && db.Statement.Table == "traffic_seqs" {
			db.AddError(errors.New("injected seq failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.St.DB.Callback().Update().Remove(cbName) }()

	p.ingestTraffic(c, &proto.TrafficDelta{
		Seq: 3, TS: now,
		Items: []proto.TrafficItem{{Scope: "server", Name: "eth0", Up: 999, Down: 999}},
	})
	up, down = p.Sum("server", sid, since)
	if up != 200 || down != 310 {
		t.Fatalf("failed transaction was not rolled back: up=%d down=%d, want 200/310", up, down)
	}
	p.St.DB.First(&seq, "agent_id = ?", sv.AgentID)
	if seq.LastSeq != 2 {
		t.Fatalf("LastSeq advanced despite rollback: %d", seq.LastSeq)
	}
}

func TestCheckTrafficQuotaAutoResolvesPriorCycle(t *testing.T) {
	p := newTestPanel(t)
	e := NewAlertEngine(p)
	p.Alert = e
	sv := addNode(t, p, "HK-Quota", "HK", "1.1.1.1", true, false)
	p.St.DB.Model(sv).Update("quota_bytes", int64(100<<30))

	// Seed a firing alert from a prior billing cycle.
	e.Fire("traffic", sv.ID, "traffic-90-20200101", "warning", "HK-Quota 本周期流量已用 90%")
	var firing int64
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND server_id = ? AND status = ?", "traffic", sv.ID, "firing").Count(&firing)
	if firing != 1 {
		t.Fatalf("seeded firing = %d, want 1", firing)
	}

	p.checkTrafficQuota(sv.ID)
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND server_id = ? AND status = ?", "traffic", sv.ID, "firing").Count(&firing)
	if firing != 0 {
		t.Fatalf("prior cycle traffic alert still firing (%d)", firing)
	}
}
