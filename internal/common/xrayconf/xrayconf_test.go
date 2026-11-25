package xrayconf

import (
	"encoding/json"
	"testing"
)

func apiServices(t *testing.T, n Node) ([]string, bool) {
	t.Helper()
	b, err := Build(n)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		API struct {
			Services []string `json:"services"`
		} `json:"api"`
		Observatory map[string]any `json:"observatory"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.API.Services, cfg.Observatory != nil
}

// Xray refuses to start when ObservatoryService is enabled without an
// observatory section, so the service must follow the balancers.
func TestObservatoryServiceFollowsBalancers(t *testing.T) {
	svcs, obs := apiServices(t, Node{Landing: &Landing{Port: 1234, Method: "2022-blake3-aes-128-gcm", ServerKey: "k"}})
	if obs {
		t.Fatal("observatory without balancers")
	}
	for _, s := range svcs {
		if s == "ObservatoryService" {
			t.Fatal("ObservatoryService enabled without observatory")
		}
	}

	svcs, obs = apiServices(t, Node{Balancers: []Balancer{{Tag: "b1", Selector: []string{"out-"}, Fallback: "out-1"}}})
	if !obs {
		t.Fatal("balancer without observatory")
	}
	found := false
	for _, s := range svcs {
		found = found || s == "ObservatoryService"
	}
	if !found {
		t.Fatalf("ObservatoryService missing: %v", svcs)
	}
}

func TestSupportsX25519MLKEM768(t *testing.T) {
	cases := []struct {
		ver  string
		want bool
	}{
		{"", true},
		{"26.9.8", true},
		{"v26.9.8", true},
		{"25.10.15", true},
		{"25.5.16", true},
		{"25.5.15", false},
		{"25.1.1", false},
		{"1.8.24", false},
	}
	for _, tc := range cases {
		if got := SupportsX25519MLKEM768(tc.ver); got != tc.want {
			t.Errorf("SupportsX25519MLKEM768(%q) = %v, want %v", tc.ver, got, tc.want)
		}
	}
}

func TestSSRFProtectionAndLandingSniffing(t *testing.T) {
	b, err := Build(Node{
		Landing: &Landing{Port: 8388, Method: "2022-blake3-aes-128-gcm", ServerKey: "k"},
		Users:   []User{{Email: "u1", UUID: "uuid-1", Outbound: "direct"}},
		BlockBT: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Sniffing *struct {
				Enabled      bool     `json:"enabled"`
				DestOverride []string `json:"destOverride"`
				RouteOnly    bool     `json:"routeOnly"`
			} `json:"sniffing"`
		} `json:"inbounds"`
		Routing struct {
			DomainStrategy string           `json:"domainStrategy"`
			Rules          []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.DomainStrategy != "IPIfNonMatch" {
		t.Fatalf("domainStrategy = %q, want IPIfNonMatch", cfg.Routing.DomainStrategy)
	}

	var foundSS bool
	for _, in := range cfg.Inbounds {
		if in.Tag == "ss-in" {
			foundSS = true
			if in.Sniffing == nil || !in.Sniffing.Enabled || !in.Sniffing.RouteOnly {
				t.Fatalf("ss-in sniffing not properly configured: %+v", in.Sniffing)
			}
			wantDest := map[string]bool{"http": true, "tls": true, "quic": true}
			for _, d := range in.Sniffing.DestOverride {
				delete(wantDest, d)
			}
			if len(wantDest) != 0 {
				t.Fatalf("ss-in destOverride missing %v: %v", wantDest, in.Sniffing.DestOverride)
			}
		}
	}
	if !foundSS {
		t.Fatal("ss-in inbound missing")
	}

	domainBlockIdx, userRuleIdx, btRuleIdx := -1, -1, -1
	for i, r := range cfg.Routing.Rules {
		if doms, ok := r["domain"].([]any); ok && r["outboundTag"] == "block" {
			set := map[string]bool{}
			for _, d := range doms {
				set[d.(string)] = true
			}
			if set["localhost"] && set["domain:localhost"] && set["full:metadata.google.internal"] {
				domainBlockIdx = i
			}
		}
		if protos, ok := r["protocol"].([]any); ok && r["outboundTag"] == "block" {
			for _, p := range protos {
				if p == "bittorrent" {
					btRuleIdx = i
				}
			}
		}
		if r["ruleTag"] == "u1" {
			userRuleIdx = i
		}
	}
	if domainBlockIdx < 0 {
		t.Fatalf("missing localhost/IMDS domain block rule in %v", cfg.Routing.Rules)
	}
	if userRuleIdx < 0 || domainBlockIdx > userRuleIdx {
		t.Fatalf("domain block rule (%d) must precede user rules (%d)", domainBlockIdx, userRuleIdx)
	}
	if btRuleIdx < 0 {
		t.Fatal("missing bittorrent block rule")
	}
}

func TestCompareVersionPreReleaseAndPrefixes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v26.9.8", "26.9.8", 0},
		{"V26.9.8+build.1", "26.9.8", 0},
		{"26.9.8-rc1", "26.9.7", 1},
		{"26.9.8-rc1", "26.9.8", -1},
		{"26.9.8-rc2", "26.9.8-rc1", 1},
		{"26.9.8-beta.10", "26.9.8-beta.2", 1},
		{"0.1.2-dirty", "0.1.1", 1},
		{"0.1.2-dirty", "0.1.2", -1},
		{"v25.10.15", "25.5.16", 1},
		{"25.5.16-rc1", "25.5.16", -1},
		{"25.5.17-rc1", "25.5.16", 1},
	}
	for _, tc := range cases {
		if got := CompareVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersion(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if !SupportsX25519MLKEM768("26.9.8-rc1") {
		t.Fatal("SupportsX25519MLKEM768(26.9.8-rc1) should be true")
	}
	if !SupportsTarget("25.1.2-dirty") {
		t.Fatal("SupportsTarget(25.1.2-dirty) should be true")
	}
}
