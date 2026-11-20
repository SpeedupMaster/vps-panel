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
