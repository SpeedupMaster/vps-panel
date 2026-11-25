package service

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestDeleteServerBackupIDsAndFullPurge(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-Del", "HK", "1.1.1.1", true, false)
	jp := addNode(t, p, "JP-Del", "JP", "2.2.2.2", false, true)
	us := addNode(t, p, "US-Del", "US", "3.3.3.3", false, true)

	r, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, BackupIDs: []uint{us.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// US is referenced only in BackupIDs: DeleteServer must reject!
	if err := p.DeleteServer(us.ID); err == nil {
		t.Fatal("DeleteServer allowed deleting backup landing server")
	}
	// Even when route is disabled, BackupIDs reference must still reject!
	if err := p.SetRouteEnabled(r.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteServer(us.ID); err == nil {
		t.Fatal("DeleteServer allowed deleting backup landing of disabled route")
	}

	// Delete the route first, then seed server-scoped rows to verify full purge.
	if err := p.DeleteRoute(r.ID); err != nil {
		t.Fatal(err)
	}
	sid := strconv.FormatUint(uint64(us.ID), 10)
	now := time.Now()
	p.St.DB.Create(&store.TrafficHourly{Scope: "server", ScopeID: sid, Hour: now.Unix() / 3600 * 3600, Up: 10, Down: 20})
	p.St.DB.Create(&store.TrafficHourly{Scope: "landing_user", ScopeID: sid + ":from-1", Hour: now.Unix() / 3600 * 3600, Up: 1, Down: 2})
	p.St.DB.Create(&store.TrafficSeq{AgentID: us.AgentID, LastSeq: 7})
	p.St.DB.Create(&store.Task{ID: nextTaskID(), ServerID: us.ID, Type: "diag", Status: "success", CreatedAt: now})
	p.St.DB.Create(&store.AlertEvent{Kind: "cpu", ServerID: us.ID, Key: "rule-1", Status: "firing", StartedAt: now})
	p.St.DB.Create(&store.ToolResult{ServerID: us.ID, Tool: "diagnose", Summary: "ok", Data: "{}", TS: now})
	_ = p.St.Set("fw_hash_"+sid, "abc123")

	if err := p.DeleteServer(us.ID); err != nil {
		t.Fatalf("DeleteServer(us): %v", err)
	}

	var cnt int64
	p.St.DB.Model(&store.TrafficHourly{}).Where("scope_id = ? OR scope_id LIKE ?", sid, sid+":%").Count(&cnt)
	if cnt != 0 {
		t.Fatalf("traffic_hourly leaked %d rows", cnt)
	}
	p.St.DB.Model(&store.TrafficSeq{}).Where("agent_id = ?", us.AgentID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("traffic_seqs leaked %d rows", cnt)
	}
	p.St.DB.Model(&store.Task{}).Where("server_id = ?", us.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("tasks leaked %d rows", cnt)
	}
	p.St.DB.Model(&store.AlertEvent{}).Where("server_id = ?", us.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("alert_events leaked %d rows", cnt)
	}
	p.St.DB.Model(&store.ToolResult{}).Where("server_id = ?", us.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("tool_results leaked %d rows", cnt)
	}
	if v := p.St.Get("fw_hash_"+sid, ""); v != "" {
		t.Fatalf("fw_hash_%s still cached: %q", sid, v)
	}
}

func TestSyncRouteLandingUserHygieneAndBackupConfigSynced(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-LU", "HK", "1.1.1.1", true, false)
	jp := addNode(t, p, "JP-LU", "JP", "2.2.2.2", false, true)
	us := addNode(t, p, "US-LU", "US", "3.3.3.3", false, true)

	r, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, BackupIDs: []uint{us.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// Mark hk and jp applied, but NOT backup us: ConfigSynced must be false!
	p.markApplied(hk.ID, p.latestVersion(hk.ID).Version)
	p.markApplied(jp.ID, p.latestVersion(jp.ID).Version)
	views := p.RouteViews()
	if len(views) != 1 || views[0].ConfigSynced {
		t.Fatalf("ConfigSynced = true while backup landing out of sync: %+v", views)
	}
	// Mark backup us applied too -> ConfigSynced becomes true.
	p.markApplied(us.ID, p.latestVersion(us.ID).Version)
	views = p.RouteViews()
	if !views[0].ConfigSynced {
		t.Fatalf("ConfigSynced = false after all nodes synced: %+v", views[0])
	}

	// Switch route from jp+us to direct: orphaned LandingUser rows on jp and us must be cleaned up.
	if _, err := p.UpdateRoute(r.ID, RouteInput{Name: r.Name, Type: "direct", EntryServerID: hk.ID}); err != nil {
		t.Fatal(err)
	}
	var luCount int64
	p.St.DB.Model(&store.LandingUser{}).Where("landing_server_id IN ?", []uint{jp.ID, us.ID}).Count(&luCount)
	if luCount != 0 {
		t.Fatalf("orphaned LandingUser rows remained: %d", luCount)
	}
}

func TestConfigVersionEncryptedAtRestAndLegacyPlaintext(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-Enc", "HK", "1.1.1.1", true, false)
	if _, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: hk.ID, Name: "r1"}); err != nil {
		t.Fatal(err)
	}
	var raw store.ConfigVersion
	if err := p.St.DB.Where("server_id = ?", hk.ID).Order("version DESC").First(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw.Config, "privateKey") || strings.Contains(raw.Config, "reality-in") {
		t.Fatal("ConfigVersion.Config stored in plaintext in SQLite!")
	}
	if !strings.HasPrefix(raw.Config, sealedConfigPrefix) {
		t.Fatalf("expected sealed prefix, got %q", raw.Config[:min(len(raw.Config), 16)])
	}
	dec := p.OpenConfigText(raw.Config)
	if !strings.Contains(dec, "reality-in") {
		t.Fatalf("OpenConfigText failed to decrypt: %q", dec)
	}
	// Legacy plaintext row is returned transparently.
	legacy := `{"inbounds":[{"tag":"reality-in"}]}`
	if got := p.OpenConfigText(legacy); got != legacy {
		t.Fatalf("legacy plaintext decode = %q, want %q", got, legacy)
	}
}

func TestNextTaskIDMonotonicAndUTF8TailTruncation(t *testing.T) {
	const N = 200
	var mu sync.Mutex
	seen := make(map[uint64]bool, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := nextTaskID()
			mu.Lock()
			seen[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != N {
		t.Fatalf("nextTaskID produced duplicates: %d unique out of %d", len(seen), N)
	}

	// UTF-8 safe tail truncation in onTaskResult.
	p := newTestPanel(t)
	sv := addNode(t, p, "HK-UTF8", "HK", "1.1.1.1", true, false)
	task, err := p.CreateTask(sv.ID, proto.TaskDiagnose, nil, nil, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("中文日志测试", (256<<10)/12+100)
	p.onTaskResult(sv.ID, &proto.TaskResult{TaskID: task.ID, Status: "success", Output: big, Finished: true})
	var saved store.Task
	p.St.DB.First(&saved, task.ID)
	if !utf8.ValidString(saved.Output) {
		t.Fatal("truncated task output is not valid UTF-8")
	}
	if len(saved.Output) > 256<<10 {
		t.Fatalf("truncated output exceeded 256KiB: %d", len(saved.Output))
	}
}

func TestAlertHydrateSinceOnRestartAndRenewedExpiryResolve(t *testing.T) {
	p := newTestPanel(t)
	e1 := NewAlertEngine(p)
	p.Alert = e1
	sv := addNode(t, p, "HK-Alert", "HK", "1.1.1.1", true, false)

	var cpu store.AlertRule
	p.St.DB.First(&cpu, "kind = ?", "cpu")
	p.St.DB.Model(&cpu).Update("duration", 0)

	// Fire a threshold rule, then simulate panel restart with a brand new AlertEngine.
	e1.Observe("cpu", sv.ID, 95)
	e2 := NewAlertEngine(p)
	p.Alert = e2

	// Metric recovers after restart: must resolve the firing event!
	e2.Observe("cpu", sv.ID, 20)
	var firing int64
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND server_id = ? AND status = ?", "cpu", sv.ID, "firing").Count(&firing)
	if firing != 0 {
		t.Fatalf("firing cpu alert did not resolve after engine restart (%d firing)", firing)
	}

	// Expiry alert resolves when server expiry date is renewed.
	soon := time.Now().Add(48 * time.Hour)
	p.St.DB.Model(sv).Update("expire_at", &soon)
	p.CheckExpiry()
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND server_id = ? AND status = ?", "expire", sv.ID, "firing").Count(&firing)
	if firing != 1 {
		t.Fatalf("expected 1 firing expire alert, got %d", firing)
	}
	renewed := time.Now().Add(60 * 24 * time.Hour)
	p.St.DB.Model(sv).Update("expire_at", &renewed)
	p.CheckExpiry()
	p.St.DB.Model(&store.AlertEvent{}).Where("kind = ? AND server_id = ? AND status = ?", "expire", sv.ID, "firing").Count(&firing)
	if firing != 0 {
		t.Fatalf("renewed server expire alert still firing (%d)", firing)
	}
}

func TestProviderURLInstallCommandAndSSHHostKeyFP(t *testing.T) {
	p := newTestPanel(t)
	if _, _, err := p.CreateServer(ServerInput{Name: "BadURL", Mode: "probe", ProviderURL: "javascript:alert(1)"}); err == nil {
		t.Fatal("javascript: provider_url was accepted")
	}
	if _, _, err := p.CreateServer(ServerInput{Name: "GoodURL", Mode: "probe", ProviderURL: "https://provider.example.com"}); err != nil {
		t.Fatalf("valid https provider_url rejected: %v", err)
	}
	_ = p.St.Set("panel_url", "https://panel.example.com")
	cmd := p.InstallCommand("tok-123", "node")
	if !strings.Contains(cmd, "'https://panel.example.com/install.sh'") || !strings.Contains(cmd, "--token 'tok-123'") {
		t.Fatalf("InstallCommand not shell-quoted: %s", cmd)
	}
	if err := verifyHostKeyFingerprint("", "ssh-ed25519", "ABCDEF"); err != nil {
		t.Fatalf("empty fingerprint should allow: %v", err)
	}
	if err := verifyHostKeyFingerprint("SHA256:ABCDEF", "ssh-ed25519", "ABCDEF"); err != nil {
		t.Fatalf("matching SHA256 fingerprint rejected: %v", err)
	}
	if err := verifyHostKeyFingerprint("ssh-ed25519 SHA256:ABCDEF", "ssh-ed25519", "ABCDEF"); err != nil {
		t.Fatalf("matching full fingerprint rejected: %v", err)
	}
	if err := verifyHostKeyFingerprint("SHA256:WRONG", "ssh-ed25519", "ABCDEF"); err == nil {
		t.Fatal("mismatching fingerprint accepted")
	}

	// agentSettings splits panel_urls_backup on both commas and newlines.
	_ = p.St.Set("panel_urls_backup", "https://b1.example.com/, https://b2.example.com\nhttps://b3.example.com/")
	as := p.agentSettings()
	if len(as.PanelURLs) != 4 || as.PanelURLs[1] != "https://b1.example.com" || as.PanelURLs[2] != "https://b2.example.com" || as.PanelURLs[3] != "https://b3.example.com" {
		t.Fatalf("agentSettings PanelURLs = %v", as.PanelURLs)
	}
}
