package service

import (
	"fmt"
	"testing"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestPerInstanceProbeAndSubLimiterIsolation(t *testing.T) {
	p1 := newTestPanel(t)
	p2 := newTestPanel(t)

	sv1 := addNode(t, p1, "P1-HK", "HK", "1.1.1.1", true, false)
	p1.ingestProbe(sv1.ID, &proto.ProbeResult{
		TS: time.Now().Unix(), Kind: "backhaul",
		Samples: []proto.ProbeSample{{Target: "zs-bj", Avg: 42, Loss: 0}},
	})
	if len(p1.ProbeMatrix("backhaul")) != 1 {
		t.Fatalf("p1 ProbeMatrix = %d, want 1", len(p1.ProbeMatrix("backhaul")))
	}
	if len(p2.ProbeMatrix("backhaul")) != 0 {
		t.Fatalf("p2 leaked p1 probe state: %+v", p2.ProbeMatrix("backhaul"))
	}

	// Exhaust subPerIPPerMin on p1; p2 must remain unaffected for the same IP.
	ip := "198.51.100.88"
	for i := 0; i < subPerIPPerMin; i++ {
		_, _ = p1.ServeSub("missing", "base64", "ua", ip)
	}
	if _, err := p1.ServeSub("missing", "base64", "ua", ip); err != ErrSubRateLimited {
		t.Fatalf("p1 expected ErrSubRateLimited, got %v", err)
	}
	if _, err := p2.ServeSub("missing", "base64", "ua", ip); err != ErrSubNotFound {
		t.Fatalf("p2 affected by p1 rate limit: got %v, want ErrSubNotFound", err)
	}
}

func TestDeleteAlertRuleResolvesEventsClearsSinceAndAudits(t *testing.T) {
	p := newTestPanel(t)
	e := NewAlertEngine(p)
	p.Alert = e

	sv := addNode(t, p, "Rule-HK", "HK", "1.1.1.1", true, false)
	rule := &store.AlertRule{Name: "CPU Custom", Kind: "cpu", Threshold: 50, Duration: 0, Severity: "warning", Scope: "all", Enabled: true}
	if err := p.SaveAlertRule(rule, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	e.Observe("cpu", sv.ID, 85)

	key := fmt.Sprintf("rule-%d", rule.ID)
	var firing int64
	p.St.DB.Model(&store.AlertEvent{}).Where("key = ? AND status = ?", key, "firing").Count(&firing)
	if firing != 1 {
		t.Fatalf("expected 1 firing event for %s, got %d", key, firing)
	}

	if err := p.DeleteAlertRule(rule.ID, "127.0.0.1"); err != nil {
		t.Fatalf("DeleteAlertRule: %v", err)
	}
	p.St.DB.Model(&store.AlertEvent{}).Where("key = ? AND status = ?", key, "firing").Count(&firing)
	if firing != 0 {
		t.Fatalf("firing event not resolved on DeleteAlertRule")
	}
	var resolved int64
	p.St.DB.Model(&store.AlertEvent{}).Where("key = ? AND status = ? AND resolved_at IS NOT NULL", key, "resolved").Count(&resolved)
	if resolved != 1 {
		t.Fatalf("expected 1 resolved event, got %d", resolved)
	}
	e.mu.Lock()
	_, hasSince := e.since[fmt.Sprintf("%d|%d", rule.ID, sv.ID)]
	e.mu.Unlock()
	if hasSince {
		t.Fatal("AlertEngine.since still holds entry for deleted rule")
	}
	var auditN int64
	p.St.DB.Model(&store.AuditLog{}).Where("type = ? AND action = ?", "alert", "delete_rule").Count(&auditN)
	if auditN != 1 {
		t.Fatalf("expected delete_rule audit log, got %d", auditN)
	}
}

func TestDeleteSubTokenCleansLogsLimiterAndAudits(t *testing.T) {
	p := newTestPanel(t)
	tok, err := p.CreateSubToken("Phone", "base64", "tok-del-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ServeSub("tok-del-1", "base64", "ua", "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	if len(p.SubAccessLogs(tok.ID, 10)) != 1 {
		t.Fatal("expected 1 access log before delete")
	}

	if err := p.DeleteSubToken(tok.ID, "127.0.0.1"); err != nil {
		t.Fatalf("DeleteSubToken: %v", err)
	}
	if len(p.SubAccessLogs(tok.ID, 10)) != 0 {
		t.Fatal("SubAccessLog rows leaked after DeleteSubToken")
	}
	p.subLim.mu.Lock()
	_, hasLim := p.subLim.hits[tok.ID]
	p.subLim.mu.Unlock()
	if hasLim {
		t.Fatal("subLimiter retained hits for deleted token")
	}
	var auditN int64
	p.St.DB.Model(&store.AuditLog{}).Where("type = ? AND action = ?", "sub", "delete_token").Count(&auditN)
	if auditN != 1 {
		t.Fatalf("expected delete_token audit log, got %d", auditN)
	}
}

func TestDeleteScriptReferentialIntegrityAndAudit(t *testing.T) {
	p := newTestPanel(t)
	sc := &store.Script{Name: "custom-check", Content: "echo ok"}
	if err := p.SaveScript(sc); err != nil {
		t.Fatal(err)
	}
	sched := &store.Schedule{Name: "run-custom", Cron: "@every 1h", Action: fmt.Sprintf("script:%d", sc.ID), Servers: "all", Enabled: true}
	if err := p.SaveSchedule(sched); err != nil {
		t.Fatal(err)
	}
	// Referenced by schedule -> must be rejected.
	if err := p.DeleteScript(sc.ID, "127.0.0.1"); err == nil {
		t.Fatal("DeleteScript allowed deleting a script referenced by a schedule")
	}
	// Remove schedule, then delete succeeds and writes audit log.
	if err := p.DeleteSchedule(sched.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteScript(sc.ID, "127.0.0.1"); err != nil {
		t.Fatalf("DeleteScript after schedule removal: %v", err)
	}
	var auditN int64
	p.St.DB.Model(&store.AuditLog{}).Where("type = ? AND action = ?", "system", "delete_script").Count(&auditN)
	if auditN != 1 {
		t.Fatalf("expected delete_script audit log, got %d", auditN)
	}
}

func TestTransactionalProbeBulkAndSubTemplates(t *testing.T) {
	p := newTestPanel(t)
	t1 := &store.ProbeTarget{Name: "T1", Host: "1.1.1.1", Port: 443, Type: "tcp"}
	t2 := &store.ProbeTarget{Name: "T2", Host: "8.8.8.8", Port: 443, Type: "tcp"}
	if err := p.CreateProbeTarget(t1); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateProbeTarget(t2); err != nil {
		t.Fatal(err)
	}
	if err := p.SetProbeTargetsBulk([]string{t2.ID}); err != nil {
		t.Fatalf("SetProbeTargetsBulk: %v", err)
	}
	list := p.ListProbeTargets("custom")
	en := map[string]bool{}
	for _, it := range list {
		en[it.ID] = it.Enabled
	}
	if en[t1.ID] || !en[t2.ID] {
		t.Fatalf("unexpected enabled state after bulk update: %+v", en)
	}

	// SaveSubTemplate keeps at most one active template per target.
	tpl1 := &store.SubTemplate{Name: "M1", Target: "mihomo", Content: "", Active: true}
	if err := p.SaveSubTemplate(tpl1); err != nil {
		t.Fatalf("SaveSubTemplate 1: %v", err)
	}
	tpl2 := &store.SubTemplate{Name: "M2", Target: "mihomo", Content: "", Active: true}
	if err := p.SaveSubTemplate(tpl2); err != nil {
		t.Fatalf("SaveSubTemplate 2: %v", err)
	}
	tpls := p.ListSubTemplates()
	activeCount := 0
	for _, x := range tpls {
		if x.Active {
			activeCount++
			if x.ID != tpl2.ID {
				t.Fatalf("expected tpl2 active, got %+v", x)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("active templates = %d, want 1", activeCount)
	}
}
