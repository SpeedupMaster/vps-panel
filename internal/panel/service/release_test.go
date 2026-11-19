package service

import (
	"os"
	"path/filepath"
	"testing"

	"vpspanel/internal/common/version"
)

func fakeELF(tag byte) []byte {
	b := make([]byte, 1<<20)
	copy(b, "\x7fELF")
	b[len(b)-1] = tag
	return b
}

func TestRegisterBundledAgent(t *testing.T) {
	p := newTestPanel(t)
	old := version.Version
	version.Version = "abc1234"
	defer func() { version.Version = old }()

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "vpsagent-linux-amd64"), fakeELF(1), 0o755); err != nil {
		t.Fatal(err)
	}
	p.RegisterBundledAgent(src)

	rs := p.Releases("agent")
	if len(rs) != 1 || rs[0].Version != "abc1234" {
		t.Fatalf("releases = %+v", rs)
	}
	if got := p.AgentCurrent(); got != "abc1234" {
		t.Fatalf("AgentCurrent = %q", got)
	}
	if _, ok := p.AgentReleaseFile("abc1234", "vpsagent-linux-amd64"); !ok {
		t.Fatal("bundled binary not served under /dl/agentv")
	}
	if err := p.DeleteAgentRelease("abc1234"); err == nil {
		t.Fatal("deleting the current bundled release should fail")
	}

	// Registering again is a no-op; a hand-published release takes precedence.
	p.RegisterBundledAgent(src)
	if n := len(p.Releases("agent")); n != 1 {
		t.Fatalf("got %d releases after re-register", n)
	}
	_ = p.St.Set("agent_published", "9.9.9")
	if got := p.AgentCurrent(); got != "9.9.9" {
		t.Fatalf("AgentCurrent with published = %q", got)
	}
}
