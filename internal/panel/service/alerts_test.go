package service

import (
	"encoding/json"
	"testing"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestAlertEngine(t *testing.T) {
	p := newTestPanel(t)
	e := NewAlertEngine(p)
	p.Alert = e
	sv := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)

	var cpu store.AlertRule
	p.St.DB.First(&cpu, "kind = ?", "cpu")
	p.St.DB.Model(&cpu).Update("duration", 0)

	count := func(status string) int64 {
		var n int64
		p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND status = ?", "cpu", status).Count(&n)
		return n
	}
	e.Observe("cpu", sv.ID, 50)
	if count("firing") != 0 {
		t.Fatal("fired below threshold")
	}
	e.Observe("cpu", sv.ID, 95)
	e.Observe("cpu", sv.ID, 97) // dedup while firing
	if count("firing") != 1 {
		t.Fatalf("firing = %d", count("firing"))
	}
	e.Observe("cpu", sv.ID, 10)
	if count("firing") != 0 || count("resolved") != 1 {
		t.Fatal("not resolved")
	}
	// fire + recovery are queued; flush clears the queue.
	if len(e.queue) != 2 {
		t.Fatalf("queue %d", len(e.queue))
	}
	e.flush()
	if len(e.queue) != 0 {
		t.Fatal("queue not flushed")
	}

	// Disabled rules suppress Fire.
	p.St.DB.Model(&store.AlertRule{}).Where("kind = ?", "offline").Update("enabled", false)
	e.Fire("offline", sv.ID, "offline", "critical", "x")
	var n int64
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ?", "offline").Count(&n)
	if n != 0 {
		t.Fatal("disabled rule fired")
	}
	// Scope: probe-only rule does not match a node.
	p.St.DB.Create(&store.AlertRule{Name: "x", Kind: "mem", Threshold: 1, Scope: "probe", Enabled: true})
	p.St.DB.Model(&store.AlertRule{}).Where("kind = ? AND scope = ?", "mem", "all").Update("enabled", false)
	e.Observe("mem", sv.ID, 99)
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ?", "mem").Count(&n)
	if n != 0 {
		t.Fatal("scope ignored")
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	if !inQuiet("23:00-07:00", at(2, 0)) || inQuiet("23:00-07:00", at(12, 0)) || !inQuiet("12:00-13:30", at(13, 0)) || inQuiet("", at(1, 0)) {
		t.Fatal("quiet hours")
	}
}

func TestBlockDetection(t *testing.T) {
	p := newTestPanel(t)
	e := NewAlertEngine(p)
	p.Alert = e
	a := addNode(t, p, "A", "HK", "1.1.1.1", true, false)
	b := addNode(t, p, "B", "JP", "2.2.2.2", true, false)
	now := time.Now().Unix()
	lost := []proto.ProbeSample{{Target: "zs-he-cm", Avg: -1, Loss: 1}, {Target: "zs-he-ct", Avg: -1, Loss: 1}}
	good := []proto.ProbeSample{{Target: "zs-he-cm", Avg: 40, Loss: 0}}
	p.ingestProbe(b.ID, &proto.ProbeResult{TS: now, Kind: "backhaul", Samples: good})
	for i := 0; i < 3; i++ {
		p.ingestProbe(a.ID, &proto.ProbeResult{TS: now + int64(i*60), Kind: "backhaul", Samples: append([]proto.ProbeSample(nil), lost...)})
	}
	var sv store.Server
	p.St.DB.First(&sv, a.ID)
	if !sv.Blocked {
		t.Fatal("not marked blocked")
	}
	var row store.ProbeResult
	p.St.DB.First(&row, "server_id = ? AND target = ?", a.ID, "zs-he-cm")
	if row.Loss != 100 {
		t.Fatalf("loss stored as %v, want percent", row.Loss)
	}
	p.ingestProbe(a.ID, &proto.ProbeResult{TS: now + 300, Kind: "backhaul", Samples: append([]proto.ProbeSample(nil), good...)})
	p.St.DB.First(&sv, a.ID)
	if sv.Blocked {
		t.Fatal("not recovered")
	}
	if bh := p.backhaulAvg(a.ID); bh != 40 {
		t.Fatalf("backhaul avg %v", bh)
	}
}

func TestBotReply(t *testing.T) {
	p := newTestPanel(t)
	addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	if r := p.BotReply("/status@mybot"); r == "" {
		t.Fatal("empty status")
	}
	if r := p.BotReply("/vps hk"); r == "" || r == "未找到 hk" {
		t.Fatal(r)
	}
	if p.BotReply("hello") != "" {
		t.Fatal("non-command answered")
	}
}

func TestZstaticSnapshot(t *testing.T) {
	var z zstaticNodes
	if err := json.Unmarshal(zstaticSnapshot, &z); err != nil || len(z.ProvinceNodes) < 30 {
		t.Fatalf("snapshot: %v %d", err, len(z.ProvinceNodes))
	}
	id, host, port := zstaticID("he-cm-v4.zstaticcdn.com:80")
	if id != "zs-he-cm" || host != "he-cm-v4.zstaticcdn.com" || port != 80 {
		t.Fatal(id, host, port)
	}
}
