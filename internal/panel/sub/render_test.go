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

func TestMihomoRealitySupportX25519MLKEM768(t *testing.T) {
	nodes := []Node{
		{Name: "default-ver", Server: "1.1.1.2", Port: 443, UUID: "u1", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US"},
		{Name: "xray-26", Server: "1.1.1.3", Port: 443, UUID: "u2", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US", XrayVersion: "26.9.8"},
		{Name: "xray-legacy", Server: "1.1.1.4", Port: 443, UUID: "u3", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US", XrayVersion: "25.1.1"},
	}
	out, err := Mihomo(nodes, "")
	if err != nil {
		t.Fatalf("Mihomo: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal mihomo: %v", err)
	}
	want := map[string]bool{
		"default-ver": true,
		"xray-26":     true,
		"xray-legacy": false,
	}
	for _, p := range cfg["proxies"].([]any) {
		pm := p.(map[string]any)
		name := fmt.Sprint(pm["name"])
		ro, ok := pm["reality-opts"].(map[string]any)
		if !ok {
			t.Fatalf("proxy %q missing reality-opts", name)
		}
		v, has := ro["support-x25519mlkem768"]
		if want[name] {
			if !has || v != true {
				t.Errorf("proxy %q reality-opts.support-x25519mlkem768 = %v (present=%v), want true", name, v, has)
			}
		} else if has {
			t.Errorf("proxy %q should omit support-x25519mlkem768 for legacy Xray, got %v", name, v)
		}
	}
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

func TestDefaultMihomoClaudeSplitFromAI(t *testing.T) {
	nodes := []Node{
		{Name: "🇭🇰 香港直连", Server: "1.1.1.2", Port: 443, UUID: "u1", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "HK"},
		{Name: "🇺🇸 美国直连", Server: "1.1.1.3", Port: 443, UUID: "u2", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US"},
		{Name: "🇯🇵 日本直连", Server: "1.1.1.4", Port: 443, UUID: "u3", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "JP"},
	}
	out, err := Mihomo(nodes, "")
	if err != nil {
		t.Fatalf("Mihomo(default): %v", err)
	}
	assertValidMihomo(t, out, nodes)

	var cfg map[string]any
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal mihomo: %v", err)
	}

	groups := mihomoGroupMap(cfg)
	claudeGroup, ok := groups["🧠 Claude"]
	if !ok {
		t.Fatalf("missing dedicated proxy-group 🧠 Claude")
	}
	claudeProxies := toStrings(claudeGroup["proxies"])
	if len(claudeProxies) < 4 || claudeProxies[0] != "🇯🇵 日本" || claudeProxies[1] != "🇺🇸 美国" {
		t.Fatalf("🧠 Claude proxies = %v, want [🇯🇵 日本, 🇺🇸 美国, ...]", claudeProxies)
	}

	rules := toStrings(cfg["rules"])
	idxCategoryAI := indexOf(rules, "GEOSITE,category-ai-!cn,🤖 AI 服务")
	if idxCategoryAI < 0 {
		t.Fatalf("missing GEOSITE,category-ai-!cn,🤖 AI 服务 rule")
	}

	wantClaudeRules := []string{
		"GEOSITE,anthropic,🧠 Claude",
		"DOMAIN-SUFFIX,anthropic.com,🧠 Claude",
		"DOMAIN-SUFFIX,claude.ai,🧠 Claude",
		"DOMAIN-SUFFIX,claude.com,🧠 Claude",
		"DOMAIN-SUFFIX,claudeusercontent.com,🧠 Claude",
		"DOMAIN-SUFFIX,clau.de,🧠 Claude",
	}
	for _, want := range wantClaudeRules {
		idx := indexOf(rules, want)
		if idx < 0 {
			t.Errorf("missing Mihomo Claude rule %q", want)
		} else if idx > idxCategoryAI {
			t.Errorf("Mihomo Claude rule %q (index %d) must precede GEOSITE,category-ai-!cn (index %d)", want, idx, idxCategoryAI)
		}
	}

	for _, r := range rules {
		if !strings.HasSuffix(r, ",🤖 AI 服务") {
			continue
		}
		lower := strings.ToLower(r)
		if strings.Contains(lower, "anthropic") || strings.Contains(lower, "claude") || strings.Contains(lower, "clau.de") {
			t.Errorf("Claude/Anthropic rule still routed to 🤖 AI 服务: %q", r)
		}
	}

	if indexOf(rules, "GEOSITE,openai,🤖 AI 服务") < 0 {
		t.Errorf("non-Claude AI rule GEOSITE,openai,🤖 AI 服务 should remain in 🤖 AI 服务")
	}
}

func TestDefaultSingBoxClaudeSplitFromAI(t *testing.T) {
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

	outs := singboxOutboundMap(cfg)
	claudeOut, ok := outs["🧠 Claude"]
	if !ok {
		t.Fatalf("missing dedicated singbox outbound 🧠 Claude")
	}
	claudeMembers := toStrings(claudeOut["outbounds"])
	if len(claudeMembers) < 3 || claudeMembers[0] != "🇯🇵 日本" || claudeMembers[1] != "🇺🇸 美国" {
		t.Fatalf("singbox 🧠 Claude outbounds = %v, want [🇯🇵 日本, 🇺🇸 美国, ...]", claudeMembers)
	}

	route, _ := cfg["route"].(map[string]any)
	rulesRaw, _ := route["rules"].([]any)

	idxCategoryAI := -1
	idxAnthropicRS := -1
	idxClaudeDomains := -1
	claudeDomainSeen := map[string]bool{}
	openAIInAI := false

	for i, rr := range rulesRaw {
		rm := rr.(map[string]any)
		ob, _ := rm["outbound"].(string)
		rs := stringOrSlice(rm["rule_set"])
		ds := toStrings(rm["domain_suffix"])

		if ob == "🤖 AI 服务" {
			if indexOf(rs, "geosite-category-ai-!cn") >= 0 {
				idxCategoryAI = i
			}
			if indexOf(rs, "geosite-openai") >= 0 {
				openAIInAI = true
			}
			if indexOf(rs, "geosite-anthropic") >= 0 {
				t.Errorf("geosite-anthropic still routed to 🤖 AI 服务")
			}
			for _, d := range ds {
				dl := strings.ToLower(d)
				if strings.Contains(dl, "anthropic") || strings.Contains(dl, "claude") || dl == "clau.de" {
					t.Errorf("Claude/Anthropic domain %q still routed to 🤖 AI 服务", d)
				}
			}
		}
		if ob == "🧠 Claude" {
			if indexOf(rs, "geosite-anthropic") >= 0 {
				idxAnthropicRS = i
			}
			if len(ds) > 0 {
				idxClaudeDomains = i
				for _, d := range ds {
					claudeDomainSeen[d] = true
				}
			}
		}
	}

	if idxCategoryAI < 0 {
		t.Fatalf("missing geosite-category-ai-!cn rule targeting 🤖 AI 服务")
	}
	if idxAnthropicRS < 0 {
		t.Errorf("missing geosite-anthropic rule targeting 🧠 Claude")
	} else if idxAnthropicRS > idxCategoryAI {
		t.Errorf("geosite-anthropic rule (index %d) must precede geosite-category-ai-!cn (index %d)", idxAnthropicRS, idxCategoryAI)
	}
	if idxClaudeDomains < 0 {
		t.Errorf("missing domain_suffix rule targeting 🧠 Claude")
	} else if idxClaudeDomains > idxCategoryAI {
		t.Errorf("Claude domain_suffix rule (index %d) must precede geosite-category-ai-!cn (index %d)", idxClaudeDomains, idxCategoryAI)
	}
	for _, wantDomain := range []string{"anthropic.com", "claude.ai", "claude.com", "claudeusercontent.com", "clau.de"} {
		if !claudeDomainSeen[wantDomain] {
			t.Errorf("missing domain_suffix %q in 🧠 Claude rule", wantDomain)
		}
	}
	if !openAIInAI {
		t.Errorf("geosite-openai should remain routed to 🤖 AI 服务")
	}
}

func TestDefaultMihomoApplePush(t *testing.T) {
	nodes := []Node{
		{Name: "🇭🇰 香港直连", Server: "1.1.1.2", Port: 443, UUID: "u1", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "HK"},
		{Name: "🇺🇸 美国直连", Server: "1.1.1.3", Port: 443, UUID: "u2", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "US"},
		{Name: "🇯🇵 日本直连", Server: "1.1.1.4", Port: 443, UUID: "u3", SNI: "example.com", PublicKey: "pk", ShortID: "sid", FP: "chrome", RegionCode: "JP"},
	}
	out, err := Mihomo(nodes, "")
	if err != nil {
		t.Fatalf("Mihomo(default): %v", err)
	}
	assertValidMihomo(t, out, nodes)

	var cfg map[string]any
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal mihomo: %v", err)
	}

	groups := mihomoGroupMap(cfg)
	pushGroup, ok := groups["🔔 苹果推送"]
	if !ok {
		t.Fatalf("missing dedicated proxy-group 🔔 苹果推送")
	}
	pushProxies := toStrings(pushGroup["proxies"])
	if len(pushProxies) < 2 || pushProxies[0] != GroupSelect {
		t.Fatalf("🔔 苹果推送 proxies = %v, want %q as default first choice", pushProxies, GroupSelect)
	}
	if indexOf(pushProxies, "DIRECT") < 0 {
		t.Errorf("🔔 苹果推送 should include DIRECT option for manual fallback: %v", pushProxies)
	}
	if indexOf(pushProxies, "🇭🇰 香港") < 0 || indexOf(pushProxies, "🇺🇸 美国") < 0 || indexOf(pushProxies, "🇯🇵 日本") < 0 {
		t.Errorf("🔔 苹果推送 should expand region groups: %v", pushProxies)
	}

	appleGroup, ok := groups["🍎 Apple 服务"]
	if !ok {
		t.Fatalf("missing proxy-group 🍎 Apple 服务")
	}
	appleProxies := toStrings(appleGroup["proxies"])
	if len(appleProxies) == 0 || appleProxies[0] != "DIRECT" {
		t.Errorf("🍎 Apple 服务 should remain DIRECT by default, got %v", appleProxies)
	}

	rules := toStrings(cfg["rules"])
	idxAppleCN := indexOf(rules, "GEOSITE,apple-cn,DIRECT")
	idxApple := indexOf(rules, "GEOSITE,apple,🍎 Apple 服务")
	if idxAppleCN < 0 {
		t.Fatalf("missing GEOSITE,apple-cn,DIRECT rule")
	}
	if idxApple < 0 {
		t.Fatalf("missing GEOSITE,apple,🍎 Apple 服务 rule")
	}

	wantPushRules := []string{
		"DOMAIN-SUFFIX,push.apple.com,🔔 苹果推送",
		"DOMAIN-SUFFIX,push-apple.com.akadns.net,🔔 苹果推送",
		"DOMAIN-SUFFIX,courier-push-apple.com.akadns.net,🔔 苹果推送",
		"DOMAIN-SUFFIX,push.apple.com.akadns.net,🔔 苹果推送",
		"AND,((DST-PORT,5223),(IP-CIDR,17.0.0.0/8,no-resolve)),🔔 苹果推送",
		"AND,((DST-PORT,2197),(IP-CIDR,17.0.0.0/8,no-resolve)),🔔 苹果推送",
	}
	for _, want := range wantPushRules {
		idx := indexOf(rules, want)
		if idx < 0 {
			t.Errorf("missing Mihomo Apple Push rule %q", want)
		} else {
			if idx > idxAppleCN {
				t.Errorf("Mihomo Apple Push rule %q (index %d) must precede GEOSITE,apple-cn,DIRECT (index %d)", want, idx, idxAppleCN)
			}
			if idx > idxApple {
				t.Errorf("Mihomo Apple Push rule %q (index %d) must precede GEOSITE,apple (index %d)", want, idx, idxApple)
			}
		}
	}

	// Verify DNS nameserver-policy routes APNs domains to overseas DoH ahead of
	// geosite:private,cn so China DNS does not hijack APNs resolution.
	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal mihomo yaml.Node: %v", err)
	}
	dnsNode := mapGet(doc.Content[0], "dns")
	policyNode := mapGet(dnsNode, "nameserver-policy")
	if policyNode == nil || policyNode.Kind != yaml.MappingNode {
		t.Fatalf("missing dns.nameserver-policy mapping")
	}
	policyKeys := make([]string, 0, len(policyNode.Content)/2)
	policyVals := map[string][]string{}
	for i := 0; i+1 < len(policyNode.Content); i += 2 {
		k := policyNode.Content[i].Value
		policyKeys = append(policyKeys, k)
		var servers []string
		_ = policyNode.Content[i+1].Decode(&servers)
		policyVals[k] = servers
	}
	idxCNPolicy := indexOf(policyKeys, "geosite:private,cn")
	if idxCNPolicy < 0 {
		t.Fatalf("missing geosite:private,cn in dns.nameserver-policy: %v", policyKeys)
	}
	wantPushDNSPolicies := []string{
		"+.push.apple.com",
		"+.push-apple.com.akadns.net",
		"+.courier-push-apple.com.akadns.net",
		"+.push.apple.com.akadns.net",
	}
	for _, key := range wantPushDNSPolicies {
		idx := indexOf(policyKeys, key)
		if idx < 0 {
			t.Errorf("missing %q in dns.nameserver-policy", key)
			continue
		}
		if idx > idxCNPolicy {
			t.Errorf("dns.nameserver-policy %q (index %d) must precede geosite:private,cn (index %d)", key, idx, idxCNPolicy)
		}
		servers := policyVals[key]
		if len(servers) == 0 {
			t.Errorf("dns.nameserver-policy %q has empty server list", key)
		}
		for _, s := range servers {
			if strings.Contains(s, "223.5.5.5") || strings.Contains(s, "1.12.12.12") || strings.Contains(s, "alidns") || strings.Contains(s, "doh.pub") {
				t.Errorf("dns.nameserver-policy %q must use overseas DNS, got %q", key, s)
			}
		}
	}

	sniffer, _ := cfg["sniffer"].(map[string]any)
	if indexOf(toStrings(sniffer["skip-domain"]), "+.push.apple.com") < 0 {
		t.Errorf("sniffer.skip-domain should keep +.push.apple.com")
	}
}

func TestDefaultSingBoxApplePush(t *testing.T) {
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

	outs := singboxOutboundMap(cfg)
	pushOut, ok := outs["🔔 苹果推送"]
	if !ok {
		t.Fatalf("missing dedicated singbox outbound 🔔 苹果推送")
	}
	pushMembers := toStrings(pushOut["outbounds"])
	if len(pushMembers) < 2 || pushMembers[0] != GroupSelect {
		t.Fatalf("singbox 🔔 苹果推送 outbounds = %v, want %q as default first choice", pushMembers, GroupSelect)
	}
	if indexOf(pushMembers, "direct") < 0 {
		t.Errorf("singbox 🔔 苹果推送 should include direct option: %v", pushMembers)
	}
	if indexOf(pushMembers, "🇭🇰 香港") < 0 || indexOf(pushMembers, "🇺🇸 美国") < 0 || indexOf(pushMembers, "🇯🇵 日本") < 0 {
		t.Errorf("singbox 🔔 苹果推送 should expand region groups: %v", pushMembers)
	}

	appleOut, ok := outs["🍎 Apple 服务"]
	if !ok {
		t.Fatalf("missing singbox outbound 🍎 Apple 服务")
	}
	appleMembers := toStrings(appleOut["outbounds"])
	if len(appleMembers) == 0 || appleMembers[0] != "direct" {
		t.Errorf("singbox 🍎 Apple 服务 should remain direct by default, got %v", appleMembers)
	}

	wantDomains := []string{
		"push.apple.com",
		"push-apple.com.akadns.net",
		"courier-push-apple.com.akadns.net",
		"push.apple.com.akadns.net",
	}

	// Verify DNS routes APNs domains to remote (overseas DoH) ahead of geosite-cn -> local.
	dns, _ := cfg["dns"].(map[string]any)
	dnsRules, _ := dns["rules"].([]any)
	idxDNSLocalCN := -1
	idxDNSPush := -1
	dnsPushDomains := map[string]bool{}
	for i, dr := range dnsRules {
		dm := dr.(map[string]any)
		srv, _ := dm["server"].(string)
		rs := stringOrSlice(dm["rule_set"])
		if srv == "local" && indexOf(rs, "geosite-cn") >= 0 {
			idxDNSLocalCN = i
		}
		if srv == "remote" {
			if ds := toStrings(dm["domain_suffix"]); len(ds) > 0 {
				idxDNSPush = i
				for _, d := range ds {
					dnsPushDomains[d] = true
				}
			}
		}
	}
	if idxDNSLocalCN < 0 {
		t.Fatalf("missing geosite-cn -> local DNS rule")
	}
	if idxDNSPush < 0 {
		t.Errorf("missing remote DNS rule for Apple Push domains")
	} else if idxDNSPush > idxDNSLocalCN {
		t.Errorf("Apple Push DNS rule (index %d) must precede geosite-cn -> local rule (index %d)", idxDNSPush, idxDNSLocalCN)
	}
	for _, d := range wantDomains {
		if !dnsPushDomains[d] {
			t.Errorf("missing domain_suffix %q in Apple Push remote DNS rule", d)
		}
	}

	// Verify route rules match APNs domains and 17.0.0.0/8 ports 5223/2197 ahead of geosite-apple@cn.
	route, _ := cfg["route"].(map[string]any)
	rulesRaw, _ := route["rules"].([]any)
	idxAppleCN := -1
	idxApple := -1
	idxPushDomains := -1
	idxPushPort := -1
	routePushDomains := map[string]bool{}
	routePushCIDRs := map[string]bool{}
	routePushPorts := map[int]bool{}

	for i, rr := range rulesRaw {
		rm := rr.(map[string]any)
		ob, _ := rm["outbound"].(string)
		rs := stringOrSlice(rm["rule_set"])
		if ob == "direct" && indexOf(rs, "geosite-apple@cn") >= 0 {
			idxAppleCN = i
		}
		if ob == "🍎 Apple 服务" && indexOf(rs, "geosite-apple") >= 0 {
			idxApple = i
		}
		if ob == "🔔 苹果推送" {
			if ds := toStrings(rm["domain_suffix"]); len(ds) > 0 {
				idxPushDomains = i
				for _, d := range ds {
					routePushDomains[d] = true
				}
			}
			if cidrs := toStrings(rm["ip_cidr"]); len(cidrs) > 0 {
				idxPushPort = i
				for _, c := range cidrs {
					routePushCIDRs[c] = true
				}
				if rawPorts, ok := rm["port"].([]any); ok {
					for _, p := range rawPorts {
						if pf, ok := p.(float64); ok {
							routePushPorts[int(pf)] = true
						}
					}
				}
			}
		}
	}
	if idxAppleCN < 0 {
		t.Fatalf("missing geosite-apple@cn -> direct route rule")
	}
	if idxApple < 0 {
		t.Fatalf("missing geosite-apple -> 🍎 Apple 服务 route rule")
	}
	if idxPushDomains < 0 {
		t.Errorf("missing domain_suffix route rule targeting 🔔 苹果推送")
	} else if idxPushDomains > idxAppleCN || idxPushDomains > idxApple {
		t.Errorf("Apple Push domain_suffix rule (index %d) must precede geosite-apple@cn (%d) and geosite-apple (%d)", idxPushDomains, idxAppleCN, idxApple)
	}
	for _, d := range wantDomains {
		if !routePushDomains[d] {
			t.Errorf("missing domain_suffix %q in 🔔 苹果推送 route rule", d)
		}
	}
	if idxPushPort < 0 {
		t.Errorf("missing ip_cidr + port route rule targeting 🔔 苹果推送")
	} else if idxPushPort > idxAppleCN || idxPushPort > idxApple {
		t.Errorf("Apple Push ip_cidr+port rule (index %d) must precede geosite-apple@cn (%d) and geosite-apple (%d)", idxPushPort, idxAppleCN, idxApple)
	}
	if !routePushCIDRs["17.0.0.0/8"] {
		t.Errorf("missing ip_cidr 17.0.0.0/8 in 🔔 苹果推送 route rule")
	}
	for _, p := range []int{5223, 2197} {
		if !routePushPorts[p] {
			t.Errorf("missing port %d in 🔔 苹果推送 ip_cidr+port route rule", p)
		}
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
		switch parts[0] {
		case "MATCH":
			target = parts[1]
		case "AND", "OR", "NOT":
			target = parts[len(parts)-1]
		default:
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

func stringOrSlice(v any) []string {
	if s, ok := v.(string); ok {
		return []string{s}
	}
	return toStrings(v)
}

func indexOf(list []string, target string) int {
	for i, s := range list {
		if s == target {
			return i
		}
	}
	return -1
}
