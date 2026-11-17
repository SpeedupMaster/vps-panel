package xraymgr

import (
	"strings"
	"testing"

	"vpspanel/internal/common/proto"
)

func TestParseStats(t *testing.T) {
	out := []byte(`{"stat":[
	{"name":"user>>>r-3>>>traffic>>>uplink","value":"100"},
	{"name":"user>>>r-3>>>traffic>>>downlink","value":250},
	{"name":"inbound>>>api>>>traffic>>>uplink","value":"9"},
	{"name":"outbound>>>direct>>>traffic>>>downlink","value":"7"}]}`)
	items := parseStats(out)
	if len(items) != 2 || items[0].Name != "r-3" || items[0].Up != 100 || items[0].Down != 250 || items[1].Scope != "outbound" {
		t.Fatalf("%+v", items)
	}
}

func TestInboundWithUser(t *testing.T) {
	full := []byte(`{"inbounds":[{"tag":"reality-in","port":443,"protocol":"vless","settings":{"clients":[{"id":"a"}],"decryption":"none"}}]}`)
	b, err := inboundWithUser(full, "reality-in", &proto.XrayUser{Email: "r-9", ID: "u", Flow: "xtls-rprx-vision"})
	if err != nil || !strings.Contains(string(b), `"email":"r-9"`) || strings.Contains(string(b), `"id":"a"`) {
		t.Fatalf("%s %v", b, err)
	}
}
func TestAduAdded(t *testing.T) {
	cases := map[string]bool{
		"result: ok\nAdded 1 user(s) in total.\n": true,
		"Added 0 user(s) in total.\n":             false,
		"failed to add user: ...":                 false,
		"":                                        true, // older Xray prints nothing on success
	}
	for out, want := range cases {
		if got := aduAdded(out); got != want {
			t.Errorf("aduAdded(%q) = %v, want %v", out, got, want)
		}
	}
}
