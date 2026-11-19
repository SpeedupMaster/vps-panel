package sub

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRegionGroupNameChinese(t *testing.T) {
	cases := []struct {
		cc   string
		want string
	}{
		{"US", "🇺🇸 美国"},
		{"us", "🇺🇸 美国"},
		{" hk ", "🇭🇰 香港"},
		{"JP", "🇯🇵 日本"},
		{"SG", "🇸🇬 新加坡"},
		{"TW", "🇹🇼 台湾"},
		{"GB", "🇬🇧 英国"},
		{"DE", "🇩🇪 德国"},
		{"OT", "🏳 其他地区"},
		{"", "🏳 其他地区"},
		{"ZZ", "🇿🇿 ZZ"},
	}
	for _, tc := range cases {
		if got := regionGroupName(tc.cc); got != tc.want {
			t.Errorf("regionGroupName(%q) = %q, want %q", tc.cc, got, tc.want)
		}
	}
}

func TestExpandRegionsAIAndDedupe(t *testing.T) {
	nodes := []Node{
		{Name: "hk-1", RegionCode: "HK"},
		{Name: "cn-1", RegionCode: "CN"},
		{Name: "mo-1", RegionCode: "MO"},
		{Name: "ru-1", RegionCode: "RU"},
		{Name: "us-1", RegionCode: "US"},
		{Name: "jp-1", RegionCode: "JP"},
	}
	codes, _ := regions(nodes)
	var regionNames, aiRegionNames []string
	for _, c := range codes {
		name := regionGroupName(c)
		regionNames = append(regionNames, name)
		if !isAIExcludedRegion(c) {
			aiRegionNames = append(aiRegionNames, name)
		}
	}
	got := expand([]string{"{{regions_ai}}", GroupSelect, "{{regions}}", GroupFallback}, []string{"hk-1", "us-1"}, regionNames, aiRegionNames)
	want := []string{"🇯🇵 日本", "🇺🇸 美国", GroupSelect, "🇨🇳 中国", "🇭🇰 香港", "🇲🇴 澳门", "🇷🇺 俄罗斯", GroupFallback}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("expand() = %v, want %v", got, want)
	}
}

func TestDefaultMihomoAntiDNSLeakAndDynamicRegions(t *testing.T) {
	nodes := []Node{
		{Name: "🇭🇰 香港直连", Server: "1.1.1.2", Port: 443, UUID: "u1", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "HK"},
		{Name: "🇺🇸 美国直连", Server: "1.1.1.3", Port: 443, UUID: "u2", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US"},
		{Name: "🇭🇰→🇯🇵 链式日本", Server: "1.1.1.2", Port: 443, UUID: "u3", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "JP"},
		{Name: "未知节点", Server: "1.1.1.4", Port: 443, UUID: "u4", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: ""},
	}
	out, err := Mihomo(nodes, "")
	if err != nil {
		t.Fatalf("Mihomo(default): %v", err)
	}
	assertValidMihomo(t, out, nodes)

	var cfg map[string]any
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal rendered mihomo: %v", err)
	}

	// Verify anti-DNS-leak invariants and Mihomo 1.19+ compatibility.
	if _, hasGlobalFP := cfg["global-client-fingerprint"]; hasGlobalFP {
		t.Errorf("global-client-fingerprint was removed in mihomo and must not be set")
	}
	if cfg["ipv6"] != false {
		t.Errorf("ipv6 = %v, want false", cfg["ipv6"])
	}
	dns, _ := cfg["dns"].(map[string]any)
	if dns["respect-rules"] != true {
		t.Errorf("dns.respect-rules = %v, want true", dns["respect-rules"])
	}
	if dns["ipv6"] != false {
		t.Errorf("dns.ipv6 = %v, want false", dns["ipv6"])
	}
	if dns["enhanced-mode"] != "fake-ip" {
		t.Errorf("dns.enhanced-mode = %v, want fake-ip", dns["enhanced-mode"])
	}
	if _, hasFallback := dns["fallback"]; hasFallback {
		t.Errorf("dns.fallback should not be set to avoid concurrent DNS leak")
	}
	ns := toStrings(dns["nameserver"])
	if len(ns) == 0 {
		t.Fatalf("dns.nameserver is empty")
	}
	for _, s := range ns {
		if strings.Contains(s, "223.5.5.5") || strings.Contains(s, "alidns") || strings.Contains(s, "doh.pub") || strings.Contains(s, "1.12.12.12") {
			t.Errorf("dns.nameserver must only use overseas DNS, got %q", s)
		}
	}
	if len(toStrings(dns["proxy-server-nameserver"])) == 0 {
		t.Errorf("dns.proxy-server-nameserver must be configured when respect-rules is true")
	}
	if len(toStrings(dns["direct-nameserver"])) == 0 {
		t.Errorf("dns.direct-nameserver must be configured")
	}

	// Verify AI group puts non-HK/CN regions ahead of 节点选择 and 🇭🇰 香港.
	groups := mihomoGroupMap(cfg)
	aiProxies := toStrings(groups["🤖 AI 服务"]["proxies"])
	if len(aiProxies) < 4 || aiProxies[0] != "🇯🇵 日本" || aiProxies[1] != "🇺🇸 美国" {
		t.Fatalf("🤖 AI 服务 proxies = %v, want [🇯🇵 日本, 🇺🇸 美国, ...]", aiProxies)
	}
	// Region ordering: real countries sorted, 其他地区 last.
	selectProxies := toStrings(groups[GroupSelect]["proxies"])
	idxJP := indexOf(selectProxies, "🇯🇵 日本")
	idxHK := indexOf(selectProxies, "🇭🇰 香港")
	idxUS := indexOf(selectProxies, "🇺🇸 美国")
	idxOT := indexOf(selectProxies, "🏳 其他地区")
	if idxHK < 0 || idxJP < 0 || idxUS < 0 || idxOT < 0 || !(idxHK < idxJP && idxJP < idxUS && idxUS < idxOT) {
		t.Errorf("unexpected region order in %s: %v", GroupSelect, selectProxies)
	}

	// Verify all GEOIP / IP-CIDR rules carry no-resolve and avoid GeoSite lists
	// missing from Loyalsoldier/v2fly GeoSite.dat (such as biliintl).
	for _, r := range toStrings(cfg["rules"]) {
		if (strings.HasPrefix(r, "GEOIP,") || strings.HasPrefix(r, "IP-CIDR,") || strings.HasPrefix(r, "IP-CIDR6,")) && !strings.HasSuffix(r, ",no-resolve") {
			t.Errorf("IP/GEOIP rule missing no-resolve: %q", r)
		}
		if strings.HasPrefix(r, "GEOSITE,biliintl,") {
			t.Errorf("GEOSITE,biliintl is missing from Loyalsoldier GeoSite.dat: %q", r)
		}
	}

	// Verify dynamic country updates when node countries change.
	changed := []Node{
		{Name: "🇸🇬 新加坡", Server: "2.2.2.2", Port: 443, UUID: "u5", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "SG"},
		{Name: "🇩🇪 德国", Server: "2.2.2.3", Port: 443, UUID: "u6", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "DE"},
	}
	out2, err := Mihomo(changed, "")
	if err != nil {
		t.Fatalf("Mihomo(changed): %v", err)
	}
	assertValidMihomo(t, out2, changed)
	var cfg2 map[string]any
	_ = yaml.Unmarshal(out2, &cfg2)
	g2 := mihomoGroupMap(cfg2)
	if _, ok := g2["🇭🇰 香港"]; ok {
		t.Errorf("stale country 🇭🇰 香港 still present after node removal")
	}
	if _, ok := g2["🇩🇪 德国"]; !ok {
		t.Errorf("new country 🇩🇪 德国 missing")
	}
	if _, ok := g2["🇸🇬 新加坡"]; !ok {
		t.Errorf("new country 🇸🇬 新加坡 missing")
	}

	// Verify edge cases: HK-only nodes and 0 nodes both render cleanly.
	hkOnly := []Node{nodes[0]}
	outHK, err := Mihomo(hkOnly, "")
	if err != nil {
		t.Fatalf("Mihomo(hkOnly): %v", err)
	}
	assertValidMihomo(t, outHK, hkOnly)

	outEmpty, err := Mihomo(nil, "")
	if err != nil {
		t.Fatalf("Mihomo(empty): %v", err)
	}
	assertValidMihomo(t, outEmpty, nil)
}

func TestDefaultSingBoxAntiDNSLeakAndDynamicRegions(t *testing.T) {
	nodes := []Node{
		{Name: "🇭🇰 香港直连", Server: "1.1.1.2", Port: 443, UUID: "u1", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "HK"},
		{Name: "🇺🇸 美国直连", Server: "1.1.1.3", Port: 443, UUID: "u2", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US"},
		{Name: "🇯🇵 日本直连", Server: "1.1.1.4", Port: 443, UUID: "u3", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "JP"},
	}
	out, err := SingBox(nodes, "")
	if err != nil {
		t.Fatalf("SingBox(default): %v", err)
	}
	assertValidSingBox(t, out, nodes)

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal singbox: %v", err)
	}
	dns, _ := cfg["dns"].(map[string]any)
	if dns["final"] != "remote" {
		t.Errorf("singbox dns.final = %v, want remote", dns["final"])
	}
	if dns["strategy"] != "ipv4_only" {
		t.Errorf("singbox dns.strategy = %v, want ipv4_only", dns["strategy"])
	}
	if dns["independent_cache"] != true {
		t.Errorf("singbox dns.independent_cache = %v, want true", dns["independent_cache"])
	}

	outs := singboxOutboundMap(cfg)
	aiOuts := toStrings(outs["🤖 AI 服务"]["outbounds"])
	if len(aiOuts) < 3 || aiOuts[0] != "🇯🇵 日本" || aiOuts[1] != "🇺🇸 美国" {
		t.Fatalf("singbox 🤖 AI 服务 outbounds = %v, want [🇯🇵 日本, 🇺🇸 美国, ...]", aiOuts)
	}

	// Edge cases: HK-only and empty nodes.
	for name, ns := range map[string][]Node{"hkOnly": {nodes[0]}, "empty": nil} {
		b, err := SingBox(ns, "")
		if err != nil {
			t.Fatalf("SingBox(%s): %v", name, err)
		}
		assertValidSingBox(t, b, ns)
	}
}

func assertValidMihomo(t *testing.T, rendered []byte, nodes []Node) {
	t.Helper()
	for _, line := range strings.Split(string(rendered), "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "#") && strings.Contains(trim, "{{") {
			t.Fatalf("unexpanded placeholder in mihomo output: %s", trim)
		}
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(rendered, &cfg); err != nil {
		t.Fatalf("invalid rendered mihomo yaml: %v", err)
	}
	valid := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, n := range nodes {
		valid[n.Name] = true
	}
	groupsRaw, _ := cfg["proxy-groups"].([]any)
	seenGroup := map[string]bool{}
	for _, gr := range groupsRaw {
		gm := gr.(map[string]any)
		name := fmt.Sprint(gm["name"])
		if seenGroup[name] {
			t.Fatalf("duplicate proxy-group: %s", name)
		}
		seenGroup[name] = true
		valid[name] = true
	}
	for _, gr := range groupsRaw {
		gm := gr.(map[string]any)
		name := fmt.Sprint(gm["name"])
		plist := toStrings(gm["proxies"])
		if len(plist) == 0 {
			t.Fatalf("proxy-group %q has empty proxies", name)
		}
		seenP := map[string]bool{}
		for _, p := range plist {
			if seenP[p] {
				t.Fatalf("proxy-group %q has duplicate entry %q", name, p)
			}
			seenP[p] = true
			if !valid[p] {
				t.Fatalf("proxy-group %q references unknown proxy/group %q", name, p)
			}
		}
	}
	for _, r := range toStrings(cfg["rules"]) {
		parts := strings.Split(r, ",")
		var target string
		if parts[0] == "MATCH" {
			target = parts[1]
		} else {
			target = parts[2]
		}
		if !valid[target] {
			t.Fatalf("rule %q references unknown target %q", r, target)
		}
	}
}

func assertValidSingBox(t *testing.T, rendered []byte, nodes []Node) {
	t.Helper()
	if strings.Contains(string(rendered), "{{") {
		t.Fatalf("unexpanded placeholder in singbox output")
	}
	var cfg map[string]any
	if err := json.Unmarshal(rendered, &cfg); err != nil {
		t.Fatalf("invalid rendered singbox json: %v", err)
	}
	outsRaw, _ := cfg["outbounds"].([]any)
	valid := map[string]bool{}
	for _, o := range outsRaw {
		om := o.(map[string]any)
		tag := fmt.Sprint(om["tag"])
		if valid[tag] {
			t.Fatalf("duplicate singbox outbound tag: %s", tag)
		}
		valid[tag] = true
	}
	for _, o := range outsRaw {
		om := o.(map[string]any)
		tag := fmt.Sprint(om["tag"])
		if raw, has := om["outbounds"]; has {
			list := toStrings(raw)
			if len(list) == 0 {
				t.Fatalf("singbox outbound %q has empty outbounds", tag)
			}
			seen := map[string]bool{}
			for _, x := range list {
				if seen[x] {
					t.Fatalf("singbox outbound %q has duplicate member %q", tag, x)
				}
				seen[x] = true
				if !valid[x] {
					t.Fatalf("singbox outbound %q references unknown tag %q", tag, x)
				}
			}
		}
	}
	route, _ := cfg["route"].(map[string]any)
	ruleSets := map[string]bool{}
	brokenURLs := []string{
		"SagerNet/sing-geosite/rule-set/geosite-biliintl.srs",
		"SagerNet/sing-geosite/rule-set/geosite-apple-cn.srs",
		"SagerNet/sing-geoip/rule-set/geoip-netflix.srs",
		"SagerNet/sing-geoip/rule-set/geoip-telegram.srs",
		"SagerNet/sing-geoip/rule-set/geoip-google.srs",
	}
	for _, rs := range route["rule_set"].([]any) {
		rsm := rs.(map[string]any)
		ruleSets[fmt.Sprint(rsm["tag"])] = true
		u := fmt.Sprint(rsm["url"])
		for _, bad := range brokenURLs {
			if strings.Contains(u, bad) {
				t.Fatalf("rule_set %q points to nonexistent upstream URL %q", rsm["tag"], u)
			}
		}
	}
	checkRuleSetRef := func(v any) {
		switch x := v.(type) {
		case string:
			if !ruleSets[x] {
				t.Fatalf("unknown rule_set reference %q", x)
			}
		case []any:
			for _, item := range x {
				s := fmt.Sprint(item)
				if !ruleSets[s] {
					t.Fatalf("unknown rule_set reference %q", s)
				}
			}
		}
	}
	dns, _ := cfg["dns"].(map[string]any)
	for _, dr := range dns["rules"].([]any) {
		drm := dr.(map[string]any)
		if rs, ok := drm["rule_set"]; ok {
			checkRuleSetRef(rs)
		}
	}
	for _, rr := range route["rules"].([]any) {
		rrm := rr.(map[string]any)
		if rs, ok := rrm["rule_set"]; ok {
			checkRuleSetRef(rs)
		}
		if ob, ok := rrm["outbound"].(string); ok && !valid[ob] {
			t.Fatalf("singbox route rule references unknown outbound %q", ob)
		}
	}
	if final, _ := route["final"].(string); !valid[final] {
		t.Fatalf("singbox route.final references unknown outbound %q", final)
	}
}

func mihomoGroupMap(cfg map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, g := range cfg["proxy-groups"].([]any) {
		gm := g.(map[string]any)
		m[fmt.Sprint(gm["name"])] = gm
	}
	return m
}

func singboxOutboundMap(cfg map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, o := range cfg["outbounds"].([]any) {
		om := o.(map[string]any)
		m[fmt.Sprint(om["tag"])] = om
	}
	return m
}

func toStrings(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func indexOf(list []string, target string) int {
	for i, s := range list {
		if s == target {
			return i
		}
	}
	return -1
}
