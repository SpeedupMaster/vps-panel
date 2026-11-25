package firewall

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	r := Build(8388, []string{"1.2.3.4", "2001:db8::1", "bad", "10.0.0.0/8"})
	for _, want := range []string{
		"set allow4 { type ipv4_addr; flags interval; auto-merge; elements = { 1.2.3.4, 10.0.0.0/8 }; }",
		"set allow6 { type ipv6_addr; flags interval; auto-merge; elements = { 2001:db8::1 }; }",
		"tcp dport 8388 counter drop",
		"udp dport 8388 ip saddr @allow4 accept",
	} {
		if !strings.Contains(r, want) {
			t.Fatalf("missing %q in\n%s", want, r)
		}
	}
	if strings.Contains(Build(0, nil), "chain") {
		t.Fatal("port 0 should only delete")
	}
}

func TestBuildDeduplicatesAndSupportsOverlappingCIDRs(t *testing.T) {
	r := Build(8388, []string{
		"10.0.0.5",
		"10.0.0.5",
		"10.0.0.5/32",
		"10.0.0.0/8",
		"10.0.0.0/8",
		"2001:db8::1",
		"2001:db8::0001",
		"2001:db8::1/128",
		"2001:db8::/32",
	})
	wantV4 := "set allow4 { type ipv4_addr; flags interval; auto-merge; elements = { 10.0.0.5, 10.0.0.0/8 }; }"
	wantV6 := "set allow6 { type ipv6_addr; flags interval; auto-merge; elements = { 2001:db8::1, 2001:db8::/32 }; }"
	if !strings.Contains(r, wantV4) {
		t.Fatalf("missing %q in\n%s", wantV4, r)
	}
	if !strings.Contains(r, wantV6) {
		t.Fatalf("missing %q in\n%s", wantV6, r)
	}
}
