package firewall

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	r := Build(8388, []string{"1.2.3.4", "2001:db8::1", "bad", "10.0.0.0/8"})
	for _, want := range []string{"elements = { 1.2.3.4, 10.0.0.0/8 }", "elements = { 2001:db8::1 }", "tcp dport 8388 counter drop", "udp dport 8388 ip saddr @allow4 accept"} {
		if !strings.Contains(r, want) {
			t.Fatalf("missing %q in\n%s", want, r)
		}
	}
	if strings.Contains(Build(0, nil), "chain") {
		t.Fatal("port 0 should only delete")
	}
}
