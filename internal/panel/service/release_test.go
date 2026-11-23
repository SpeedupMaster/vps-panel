package service

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRegisterBundledAgentGzipNoDuplicate(t *testing.T) {
	p := newTestPanel(t)
	old := version.Version
	version.Version = "2.3.4"
	defer func() { version.Version = old }()

	raw := fakeELF(7)
	wantSum := sha256.Sum256(raw)
	wantHex := hex.EncodeToString(wantSum[:])

	src := t.TempDir()
	gzPath := filepath.Join(src, "vpsagent-linux-amd64.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	p.RegisterBundledAgent(src)

	rs := p.Releases("agent")
	if len(rs) != 1 || rs[0].Version != "2.3.4" {
		t.Fatalf("releases = %+v", rs)
	}
	assets := map[string]AgentAsset{}
	if err := json.Unmarshal([]byte(rs[0].Assets), &assets); err != nil {
		t.Fatal(err)
	}
	a, ok := assets["amd64"]
	if !ok || a.SHA256 != wantHex || a.Size != int64(len(raw)) {
		t.Fatalf("asset = %+v, want sha=%s size=%d", a, wantHex, len(raw))
	}

	// Bundled binary should NOT be duplicated under DataDir/agent/v2.3.4.
	if _, err := os.Stat(filepath.Join(p.DataDir, "agent", "v2.3.4", "vpsagent-linux-amd64")); err == nil {
		t.Fatal("bundled agent should not be duplicated into DataDir/agent/v<ver>")
	}

	path, ok := p.AgentReleaseFile("2.3.4", "vpsagent-linux-amd64")
	if !ok || !strings.HasSuffix(path, ".gz") {
		t.Fatalf("AgentReleaseFile = (%q, %v), want .gz path", path, ok)
	}

	// Resolving current agent file when no custom release is published returns bundled .gz.
	cur, ok := p.ResolveAgentFile("vpsagent-linux-amd64")
	if !ok || cur != gzPath {
		t.Fatalf("ResolveAgentFile = (%q, %v), want %q", cur, ok, gzPath)
	}
}
