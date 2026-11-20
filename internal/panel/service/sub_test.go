package service

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"vpspanel/internal/panel/store"
)

func TestSubNodesPickedRoutes(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	sg := addNode(t, p, "SG-01", "SG", "4.4.4.4", true, false)
	jp := addNode(t, p, "JP-01", "JP", "2.2.2.2", false, true)

	r1, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: hk.ID, Name: "hk"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Name: "hk-jp"})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: sg.ID, Name: "sg"})
	if err != nil {
		t.Fatal(err)
	}

	count := func() int { return len(p.SubNodes()) }

	if n := count(); n != 3 {
		t.Fatalf("no picking: %d nodes, want 3", n)
	}

	p.St.Set("sub_routes", fmt.Sprintf("%d,%d", r2.ID, r3.ID))
	nodes := p.SubNodes()
	if len(nodes) != 2 || nodes[0].UUID != r2.UUID || nodes[1].UUID != r3.UUID {
		t.Fatalf("picked r2,r3: %+v", nodes)
	}

	// The blocked filter still applies on top of the picked set.
	p.St.DB.Model(sg).Update("blocked", true)
	if n := count(); n != 1 {
		t.Fatalf("picked + blocked: %d nodes, want 1", n)
	}

	// A picked route that leaves the subscription (in_sub off) stays out.
	p.St.DB.Model(r2).Update("in_sub", false)
	if n := count(); n != 0 {
		t.Fatalf("picked route not in_sub: %d nodes, want 0", n)
	}

	// Only deleted routes picked: nothing, not everything.
	p.St.Set("sub_routes", "999")
	if n := count(); n != 0 {
		t.Fatalf("stale pick: %d nodes, want 0", n)
	}

	// Cleared = every route again (minus the filters).
	p.St.Set("sub_routes", "")
	nodes = p.SubNodes()
	if len(nodes) != 1 || nodes[0].UUID != r1.UUID {
		t.Fatalf("cleared: %+v", nodes)
	}
}

func TestSubBaseNormalization(t *testing.T) {
	p := newTestPanel(t)
	cases := []struct {
		panelURL string
		subURL   string
		want     string
	}{
		{"https://panel.example.com/", "", "https://panel.example.com"},
		{"https://panel.example.com", "https://sub.example.com/", "https://sub.example.com"},
		{"https://panel.example.com", "  sub.example.com/ ", "https://sub.example.com"},
		{"https://panel.example.com", "http://sub.example.com:8080/", "http://sub.example.com:8080"},
		{"panel.example.com", "", "https://panel.example.com"},
		{"", "", ""},
	}
	for _, tc := range cases {
		_ = p.St.Set("panel_url", tc.panelURL)
		_ = p.St.Set("sub_url", tc.subURL)
		if got := p.SubBase(); got != tc.want {
			t.Errorf("SubBase(panel=%q, sub=%q) = %q, want %q", tc.panelURL, tc.subURL, got, tc.want)
		}
	}
}

func TestSubOnlyHost(t *testing.T) {
	p := newTestPanel(t)
	cases := []struct {
		panelURL string
		subURL   string
		want     string
	}{
		{"https://panel.example.com", "", ""},
		{"", "https://sub.example.com", ""},
		{"https://panel.example.com", "https://panel.example.com", ""},
		{"https://Panel.Example.com", "https://panel.example.com:8443", ""},
		{"https://panel.example.com", "https://Sub.Example.com:8443/", "sub.example.com"},
		{"https://panel.example.com", "sub.example.com", "sub.example.com"},
	}
	for _, tc := range cases {
		_ = p.St.Set("panel_url", tc.panelURL)
		_ = p.St.Set("sub_url", tc.subURL)
		if got := p.SubOnlyHost(); got != tc.want {
			t.Errorf("SubOnlyHost(panel=%q, sub=%q) = %q, want %q", tc.panelURL, tc.subURL, got, tc.want)
		}
	}
}

func TestCustomSubTokenCreateAndUpdate(t *testing.T) {
	p := newTestPanel(t)
	_ = p.St.Set("panel_url", "https://panel.example.com")
	_ = p.St.Set("sub_url", "https://sub.example.com")
	_ = p.St.Set("admin_path", "/secret")

	// Custom suffix on create: URL has no /s/ prefix.
	t1, err := p.CreateSubToken("手机", "mihomo", "my-phone.yaml", "")
	if err != nil {
		t.Fatalf("CreateSubToken custom: %v", err)
	}
	views := p.SubTokens()
	if len(views) != 1 || views[0].Token != "my-phone.yaml" || views[0].URL != "https://sub.example.com/my-phone.yaml" {
		t.Fatalf("unexpected views: %+v", views)
	}
	if _, err := p.ServeSub("my-phone.yaml", "base64", "curl/8", "1.2.3.4"); err != nil {
		t.Fatalf("ServeSub custom token: %v", err)
	}

	// Empty custom suffix falls back to random token without /s/.
	t2, err := p.CreateSubToken("电脑", "", "", "")
	if err != nil {
		t.Fatalf("CreateSubToken random: %v", err)
	}
	views = p.SubTokens()
	if len(views) != 2 || len(views[1].Token) < 24 || views[1].URL != "https://sub.example.com/"+views[1].Token {
		t.Fatalf("unexpected random view: %+v", views[1])
	}

	// Duplicate custom suffix rejected.
	if _, err := p.CreateSubToken("重复", "", "my-phone.yaml", ""); err == nil {
		t.Fatal("duplicate custom token should fail")
	}

	// Reserved and invalid suffixes rejected.
	for _, bad := range []string{
		"api", "API", "s", "dl", "status", "healthz", "install.sh",
		"login", "offline", "servers", "routes", "topology", "latency",
		"subscription", "updates", "alerts", "tools", "settings",
		"assets", "index.html", "favicon.ico", "favicon.svg", "sw.js", "manifest.webmanifest",
		"secret", "SECRET", // matches admin_path
		".hidden", "..", "a/b", "has space", "中文", "bad?q=1",
	} {
		if _, err := p.CreateSubToken("bad", "", bad, ""); err == nil {
			t.Errorf("CreateSubToken(%q) succeeded, want error", bad)
		}
	}

	// Update without token preserves token while changing name/target/enabled.
	if err := p.UpdateSubToken(t1.ID, "我的手机", "singbox", false, nil, nil); err != nil {
		t.Fatalf("UpdateSubToken keep token: %v", err)
	}
	views = p.SubTokens()
	if views[0].Name != "我的手机" || views[0].TargetDefault != "singbox" || views[0].Enabled || views[0].Token != "my-phone.yaml" {
		t.Fatalf("after meta update: %+v", views[0])
	}

	// Update with custom token rotates the suffix; old suffix stops working.
	next := "iphone_17"
	if err := p.UpdateSubToken(t1.ID, "我的手机", "singbox", true, &next, nil); err != nil {
		t.Fatalf("UpdateSubToken change token: %v", err)
	}
	if _, err := p.ServeSub("my-phone.yaml", "base64", "curl/8", "1.2.3.4"); err != ErrSubNotFound {
		t.Fatalf("old token still accepted: %v", err)
	}
	if _, err := p.ServeSub("iphone_17", "base64", "curl/8", "1.2.3.4"); err != nil {
		t.Fatalf("new token rejected: %v", err)
	}

	// Updating to another device's token or empty string fails.
	dup := views[1].Token
	if err := p.UpdateSubToken(t1.ID, "我的手机", "singbox", true, &dup, nil); err == nil {
		t.Fatal("updating to duplicate token should fail")
	}
	empty := "   "
	if err := p.UpdateSubToken(t1.ID, "我的手机", "singbox", true, &empty, nil); err == nil {
		t.Fatal("updating to empty token should fail")
	}
	// Updating to own current token succeeds.
	same := "iphone_17"
	if err := p.UpdateSubToken(t1.ID, "我的手机", "singbox", true, &same, nil); err != nil {
		t.Fatalf("updating with same token failed: %v", err)
	}
	_ = t2
}

func TestSubUserinfoExcludesProbeAndSupportsSubMode(t *testing.T) {
	p := newTestPanel(t)
	now := time.Now()
	hour := now.Unix() / 3600 * 3600

	probe, _, err := p.CreateServer(ServerInput{Name: "Probe-01", Mode: "probe", QuotaGB: 1000})
	if err != nil {
		t.Fatal(err)
	}
	hk := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	jp := addNode(t, p, "JP-01", "JP", "2.2.2.2", false, true)
	idle := addNode(t, p, "SG-Idle", "SG", "3.3.3.3", true, false)

	p.St.DB.Model(hk).Update("quota_bytes", int64(100<<30))
	p.St.DB.Model(jp).Update("quota_bytes", int64(200<<30))
	p.St.DB.Model(idle).Update("quota_bytes", int64(500<<30))

	addTraffic := func(sv *store.Server, up, down uint64) {
		if err := p.St.DB.Create(&store.TrafficHourly{
			Scope: "server", ScopeID: strconv.FormatUint(uint64(sv.ID), 10), Hour: hour, Up: up, Down: down,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	addTraffic(probe, 100, 200)
	addTraffic(hk, 10, 20)
	addTraffic(jp, 30, 40)
	addTraffic(idle, 50, 60)

	if _, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Name: "hk-jp"}); err != nil {
		t.Fatal(err)
	}

	// Default ("all") sums node servers only and excludes probe-only servers.
	wantAll := fmt.Sprintf("upload=%d; download=%d; total=%d", 90, 120, uint64(800)<<30)
	if got := p.subUserinfo(); got != wantAll {
		t.Fatalf("subUserinfo(all) = %q, want %q", got, wantAll)
	}

	// "sub" sums only node servers referenced by active in-subscription routes.
	_ = p.St.Set("sub_userinfo", "sub")
	wantSub := fmt.Sprintf("upload=%d; download=%d; total=%d", 40, 60, uint64(300)<<30)
	if got := p.subUserinfo(); got != wantSub {
		t.Fatalf("subUserinfo(sub) = %q, want %q", got, wantSub)
	}

	// Single server selection still works.
	_ = p.St.Set("sub_userinfo", strconv.FormatUint(uint64(hk.ID), 10))
	wantHK := fmt.Sprintf("upload=%d; download=%d; total=%d", 10, 20, uint64(100)<<30)
	if got := p.subUserinfo(); got != wantHK {
		t.Fatalf("subUserinfo(hk) = %q, want %q", got, wantHK)
	}

	// "none" omits the header.
	_ = p.St.Set("sub_userinfo", "none")
	if got := p.subUserinfo(); got != "" {
		t.Fatalf("subUserinfo(none) = %q, want empty", got)
	}
}

func TestSubTokenPerLinkRoutes(t *testing.T) {
	p := newTestPanel(t)
	now := time.Now()
	hour := now.Unix() / 3600 * 3600

	hk := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	sg := addNode(t, p, "SG-01", "SG", "4.4.4.4", true, false)
	jp := addNode(t, p, "JP-01", "JP", "2.2.2.2", false, true)

	p.St.DB.Model(hk).Update("quota_bytes", int64(100<<30))
	p.St.DB.Model(sg).Update("quota_bytes", int64(200<<30))
	p.St.DB.Model(jp).Update("quota_bytes", int64(300<<30))

	addTraffic := func(sv *store.Server, up, down uint64) {
		if err := p.St.DB.Create(&store.TrafficHourly{
			Scope: "server", ScopeID: strconv.FormatUint(uint64(sv.ID), 10), Hour: hour, Up: up, Down: down,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	addTraffic(hk, 10, 20)
	addTraffic(sg, 30, 40)
	addTraffic(jp, 50, 60)

	r1, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: hk.ID, Name: "hk"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Name: "hk-jp"})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: sg.ID, Name: "sg"})
	if err != nil {
		t.Fatal(err)
	}

	// Token with empty routes follows global setting; token with custom routes overrides it.
	tGlobal, err := p.CreateSubToken("通用", "", "tok-global", "")
	if err != nil {
		t.Fatal(err)
	}
	tCustom, err := p.CreateSubToken("专享", "", "tok-custom", fmt.Sprintf(" %d , %d , %d ", r3.ID, r1.ID, r3.ID))
	if err != nil {
		t.Fatal(err)
	}
	if tCustom.Routes != fmt.Sprintf("%d,%d", r3.ID, r1.ID) {
		t.Fatalf("normalized routes = %q, want %q", tCustom.Routes, fmt.Sprintf("%d,%d", r3.ID, r1.ID))
	}
	if _, err := p.CreateSubToken("bad", "", "tok-bad", "abc"); err == nil {
		t.Fatal("invalid routes should fail on create")
	}

	// Restrict global sub_routes to r2 only: tGlobal follows global (r2), tCustom overrides (r1, r3).
	// Even under default sub_userinfo ("all"), route selection scopes userinfo to those routes' servers.
	_ = p.St.Set("sub_routes", strconv.FormatUint(uint64(r2.ID), 10))
	_ = p.St.Set("sub_userinfo", "all")

	decodeBase64 := func(b []byte) string {
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("base64 decode %q: %v", string(b), err)
		}
		return string(dec)
	}

	resGlobal, err := p.ServeSub("tok-global", "base64", "curl/8", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	bodyGlobal := decodeBase64(resGlobal.Body)
	if !strings.Contains(bodyGlobal, r2.UUID) || strings.Contains(bodyGlobal, r1.UUID) || strings.Contains(bodyGlobal, r3.UUID) {
		t.Fatalf("tGlobal body unexpected: %s", bodyGlobal)
	}
	// r2 uses hk + jp (400G total, not all 600G)
	wantGlobalUI := fmt.Sprintf("upload=%d; download=%d; total=%d", 60, 80, uint64(400)<<30)
	if got := resGlobal.Headers["Subscription-Userinfo"]; got != wantGlobalUI {
		t.Fatalf("tGlobal userinfo (all + global sub_routes) = %q, want %q", got, wantGlobalUI)
	}

	// Clearing global sub_routes: tGlobal gets all node servers (600G), while tCustom still scopes to r1+r3 (300G).
	_ = p.St.Set("sub_routes", "")
	resGlobalAll, err := p.ServeSub("tok-global", "base64", "curl/8", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	wantAllUI := fmt.Sprintf("upload=%d; download=%d; total=%d", 90, 120, uint64(600)<<30)
	if got := resGlobalAll.Headers["Subscription-Userinfo"]; got != wantAllUI {
		t.Fatalf("tGlobal userinfo (all, no pick) = %q, want %q", got, wantAllUI)
	}

	resCustom, err := p.ServeSub("tok-custom", "base64", "curl/8", "1.1.1.2")
	if err != nil {
		t.Fatal(err)
	}
	bodyCustom := decodeBase64(resCustom.Body)
	if !strings.Contains(bodyCustom, r1.UUID) || !strings.Contains(bodyCustom, r3.UUID) || strings.Contains(bodyCustom, r2.UUID) {
		t.Fatalf("tCustom body unexpected: %s", bodyCustom)
	}
	// r1 + r3 use hk + sg (300G total, even with sub_userinfo="all")
	wantCustomUI := fmt.Sprintf("upload=%d; download=%d; total=%d", 40, 60, uint64(300)<<30)
	if got := resCustom.Headers["Subscription-Userinfo"]; got != wantCustomUI {
		t.Fatalf("tCustom userinfo (all + per-token routes) = %q, want %q", got, wantCustomUI)
	}

	// Restore global sub_routes to r2 for preview and fallback checks below.
	_ = p.St.Set("sub_routes", strconv.FormatUint(uint64(r2.ID), 10))
	_ = p.St.Set("sub_userinfo", "sub")
	if got := p.subUserinfoFor(tCustom); got != wantCustomUI {
		t.Fatalf("tCustom userinfo (sub) = %q, want %q", got, wantCustomUI)
	}

	// PreviewSubFor matches global (0) vs token-specific routes.
	if content, n, err := p.PreviewSubFor("base64", 0); err != nil || n != 1 || !strings.Contains(content, r2.UUID) {
		t.Fatalf("PreviewSubFor(0) = n=%d err=%v content=%s", n, err, content)
	}
	if content, n, err := p.PreviewSubFor("base64", tCustom.ID); err != nil || n != 2 || !strings.Contains(content, r1.UUID) || !strings.Contains(content, r3.UUID) {
		t.Fatalf("PreviewSubFor(tCustom) = n=%d err=%v content=%s", n, err, content)
	}

	// Updating without routes (nil) preserves existing custom routes.
	if err := p.UpdateSubToken(tCustom.ID, "专享改名", "mihomo", true, nil, nil); err != nil {
		t.Fatal(err)
	}
	var reloaded store.SubToken
	p.St.DB.First(&reloaded, tCustom.ID)
	if reloaded.Routes != fmt.Sprintf("%d,%d", r3.ID, r1.ID) {
		t.Fatalf("routes lost on nil update: %q", reloaded.Routes)
	}

	// Blocked filter and in_sub=false still apply on top of token routes.
	p.St.DB.Model(sg).Update("blocked", true)
	if nodes := p.SubNodesFor(&reloaded); len(nodes) != 1 || nodes[0].UUID != r1.UUID {
		t.Fatalf("blocked filter on token routes: %+v", nodes)
	}
	p.St.DB.Model(r1).Update("in_sub", false)
	if nodes := p.SubNodesFor(&reloaded); len(nodes) != 0 {
		t.Fatalf("in_sub=false filter on token routes: %+v", nodes)
	}

	// Stale/deleted route IDs return 0 nodes, not global fallback.
	stale := "999"
	if err := p.UpdateSubToken(tCustom.ID, "专享改名", "mihomo", true, nil, &stale); err != nil {
		t.Fatal(err)
	}
	p.St.DB.First(&reloaded, tCustom.ID)
	if nodes := p.SubNodesFor(&reloaded); len(nodes) != 0 {
		t.Fatalf("stale token routes leaked nodes: %+v", nodes)
	}

	// Clearing routes to "" restores global fallback (r2).
	empty := ""
	if err := p.UpdateSubToken(tCustom.ID, "专享改名", "mihomo", true, nil, &empty); err != nil {
		t.Fatal(err)
	}
	p.St.DB.First(&reloaded, tCustom.ID)
	if nodes := p.SubNodesFor(&reloaded); len(nodes) != 1 || nodes[0].UUID != r2.UUID {
		t.Fatalf("cleared token routes = %+v, want r2", nodes)
	}
	_ = tGlobal
}

func TestSubProfileNameHeaders(t *testing.T) {
	p := newTestPanel(t)
	if _, err := p.CreateSubToken("手机", "", "my-sub", ""); err != nil {
		t.Fatal(err)
	}

	// Default ASCII name: plain Profile-Title + dual Content-Disposition parameters.
	res, err := p.ServeSub("my-sub", "mihomo", "clash.meta/1.19", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Headers["Profile-Title"]; got != "vps-panel" {
		t.Fatalf("default Profile-Title = %q, want vps-panel", got)
	}
	wantDefaultCD := `attachment; filename="vps-panel"; filename*=UTF-8''vps-panel`
	if got := res.Headers["Content-Disposition"]; got != wantDefaultCD {
		t.Fatalf("default Content-Disposition = %q, want %q", got, wantDefaultCD)
	}

	// Non-ASCII Chinese name: base64-encoded Profile-Title + percent-escaped Content-Disposition.
	_ = p.St.Set("sub_profile_name", " 加速师 ")
	res, err = p.ServeSub("my-sub", "mihomo", "clash.meta/1.19", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	wantTitle := "base64:" + base64.StdEncoding.EncodeToString([]byte("加速师"))
	if got := res.Headers["Profile-Title"]; got != wantTitle {
		t.Fatalf("chinese Profile-Title = %q, want %q", got, wantTitle)
	}
	wantCD := `attachment; filename="%E5%8A%A0%E9%80%9F%E5%B8%88"; filename*=UTF-8''%E5%8A%A0%E9%80%9F%E5%B8%88`
	if got := res.Headers["Content-Disposition"]; got != wantCD {
		t.Fatalf("chinese Content-Disposition = %q, want %q", got, wantCD)
	}
}
