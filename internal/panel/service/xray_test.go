package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

func newTestPanel(t *testing.T) *Panel {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db, err := st.DB.DB(); err == nil {
			db.Close()
		}
	})
	box, err := secret.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box, auth.New(st, box), hub.New(st, box), dir)
}

func addNode(t *testing.T, p *Panel, name, cc, ip string, reality, landing bool) *store.Server {
	sv, _, err := p.CreateServer(ServerInput{Name: name, Mode: "node", CapReality: reality, CapLanding: landing, CountryCode: cc})
	if err != nil {
		t.Fatal(err)
	}
	p.St.DB.Model(sv).Updates(map[string]any{"ipv4": ip, "xray_version": "25.10.15"})
	return sv
}

func TestChainConfigAndSubscription(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	jp := addNode(t, p, "JP-01", "JP", "2.2.2.2", false, true)
	us := addNode(t, p, "US-01", "US", "3.3.3.3", false, true)

	if _, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: hk.ID}); err != nil {
		t.Fatal(err)
	}
	r2, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, BackupIDs: []uint{us.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Email != "r-2" {
		t.Fatalf("email %s", r2.Email)
	}

	raw, _, err := p.BuildConfig(hk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	s := string(raw)
	for _, want := range []string{`"target":"www.microsoft.com:443"`, `"balancerTag":"bal-r-2"`, `"outboundTag":"direct"`, `"tag":"to-2-jp01"`, `"tag":"to-3-us01"`, `"observatory"`, `"ruleTag":"r-1"`} {
		if !strings.Contains(s, want) {
			t.Errorf("entry config missing %s\n%s", want, s)
		}
	}

	lraw, fw, err := p.BuildConfig(jp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lraw), `"email":"from-1"`) || fw == nil || len(fw.Allowed) != 1 || fw.Allowed[0] != "1.1.1.1" {
		t.Fatalf("landing config %s fw %+v", lraw, fw)
	}
	// SS password on the entry = serverKey:userKey of the landing user.
	var li store.LandingInbound
	p.St.DB.First(&li, "server_id = ?", jp.ID)
	sk, _ := p.Box.Open(li.ServerKeyEnc)
	if !strings.Contains(s, sk+":") {
		t.Fatal("entry outbound does not use landing server key")
	}
	if k, _ := base64.StdEncoding.DecodeString(sk); len(k) != 16 {
		t.Fatalf("ss key length %d", len(k))
	}

	// Versions were recorded once per changed node.
	var n int64
	p.St.DB.Model(&store.ConfigVersion{}).Where("server_id = ?", hk.ID).Count(&n)
	if n != 2 {
		t.Fatalf("entry versions = %d", n)
	}

	// A plain direct route on an applied config is a hot add.
	p.markApplied(hk.ID, p.latestVersion(hk.ID).Version)
	r3 := &store.Route{ID: 99, Type: "direct", EntryServerID: hk.ID, Enabled: true, Email: "r-99", UUID: NewUUID()}
	if op := p.hotOp(r3, nil); op == nil || op.Op != "add_user" || len(op.Rule) == 0 {
		t.Fatalf("expected hot add, got %+v", op)
	}
	r4 := &store.Route{ID: 98, Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Enabled: true, Email: "r-98"}
	if op := p.hotOp(r4, nil); op == nil {
		t.Fatal("chain to an existing outbound should be hot")
	}

	// Subscriptions
	nodes := p.SubNodes()
	if len(nodes) != 2 || !strings.Contains(nodes[1].Name, "🇭🇰→🇯🇵") {
		t.Fatalf("nodes %+v", nodes)
	}
	y, err := p.PreviewSub("mihomo")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	groups := m["proxy-groups"].([]any)
	if len(m["proxies"].([]any)) != 2 || groups[0].(map[string]any)["name"] != "节点选择" {
		t.Fatalf("mihomo:\n%s", y)
	}
	var ai map[string]any
	for _, g := range groups {
		if g.(map[string]any)["name"] == "🤖 AI 服务" {
			ai = g.(map[string]any)
		}
	}
	if ai == nil {
		t.Fatalf("AI group missing from %+v", groups)
	}
	aiProxies := ai["proxies"].([]any)
	// 🇯🇵 日本 (non-HK first via {{regions_ai}}), 节点选择, 🇭🇰 香港, 故障转移, + 2 nodes
	if len(aiProxies) != 6 || aiProxies[0] != "🇯🇵 日本" || aiProxies[1] != "节点选择" || aiProxies[2] != "🇭🇰 香港" {
		t.Fatalf("AI group %+v", ai)
	}
	js, err := p.PreviewSub("singbox")
	if err != nil || !json.Valid([]byte(js)) || !strings.Contains(js, `"public_key"`) {
		t.Fatalf("singbox %v\n%s", err, js)
	}
	links, _ := p.PreviewSub("base64")
	if !strings.HasPrefix(links, "vless://") || !strings.Contains(links, "pbk=") {
		t.Fatal(links)
	}

	// Token serving + stranger detection
	tok, err := p.CreateSubToken("phone", "", "")
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := p.Box.Open(tok.TokenEnc)
	res, err := p.ServeSub(plain, "", "clash.meta/1.19", "8.8.8.8")
	if err != nil || !strings.Contains(res.ContentType, "yaml") || res.Headers["Subscription-Userinfo"] == "" {
		t.Fatalf("serve %v %+v", err, res)
	}
	if _, err := p.ServeSub("wrong", "", "", "8.8.8.8"); err != ErrSubNotFound {
		t.Fatal("bad token accepted")
	}
	p.ServeSub(plain, "base64", "x", "9.9.9.9")
	var strangers int64
	p.St.DB.Model(&store.SubAccessLog{}).Where("stranger = ?", true).Count(&strangers)
	if strangers != 1 {
		t.Fatalf("strangers = %d", strangers)
	}

	// Rotate changes the UUIDs
	old := r2.UUID
	if err := p.RotateAll(); err != nil {
		t.Fatal(err)
	}
	var after store.Route
	p.St.DB.First(&after, r2.ID)
	if after.UUID == old {
		t.Fatal("uuid not rotated")
	}
}

func TestLineDiff(t *testing.T) {
	d := LineDiff("a\nb\nc\n", "a\nB\nc\n")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+B") {
		t.Fatal(d)
	}
	if LineDiff("x", "x") != "" {
		t.Fatal("equal inputs")
	}
}

func TestRealityKeys(t *testing.T) {
	priv, pub := NewX25519()
	got, err := RealityPublicKey(priv)
	if err != nil || got != pub || len(pub) != 43 {
		t.Fatal(priv, pub, got)
	}
}

func TestPrivatePeers(t *testing.T) {
	if got := privatePeers("10.0.0.5"); len(got) != 4 {
		t.Fatalf("private v4: %v", got)
	}
	if got := privatePeers("100.80.1.2"); len(got) != 4 {
		t.Fatalf("cgnat: %v", got)
	}
	if got := privatePeers("fd00::5"); len(got) != 2 {
		t.Fatalf("ula: %v", got)
	}
	for _, a := range []string{"", "203.0.113.9", "land.example.com"} {
		if got := privatePeers(a); got != nil {
			t.Fatalf("%q should add nothing: %v", a, got)
		}
	}
}

func TestRealityTarget(t *testing.T) {
	cases := []struct {
		target, names         string
		wantTarget, wantNames string
		wantErr               bool
	}{
		{"www.apple.com", "", "www.apple.com:443", "www.apple.com", false},
		{" www.apple.com:8443 ", "", "www.apple.com:8443", "www.apple.com", false},
		{"www.microsoft.com:443", "a.example.com, b.example.com", "www.microsoft.com:443", "a.example.com,b.example.com", false},
		{"", "a.example.com", "", "", true},
		{"https://www.apple.com", "", "", "", true},
		{"www.apple.com", "https://www.apple.com", "", "", true},
		{"www.apple.com", "bad_name.com", "", "", true},
	}
	for _, c := range cases {
		gt, gn, err := realityTarget(c.target, c.names)
		if (err != nil) != c.wantErr || gt != c.wantTarget || gn != c.wantNames {
			t.Errorf("realityTarget(%q, %q) = %q, %q, %v", c.target, c.names, gt, gn, err)
		}
	}
}
