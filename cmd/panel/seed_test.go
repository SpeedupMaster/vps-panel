package main

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpspanel/internal/common/version"
	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func TestBundledAgentDirAndRegister(t *testing.T) {
	explicit := t.TempDir()
	if got := bundledAgentDir(explicit); got != explicit {
		t.Fatalf("bundledAgentDir(explicit) = %q, want %q", got, explicit)
	}

	// Build a minimal panel + bundled directory with both .gz and plain ELF binaries.
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := secret.Load(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	p := service.New(st, box, auth.New(st, box), hub.New(st, box), dataDir)

	elf := append([]byte("\x7fELF"), make([]byte, 1<<20)...)
	// amd64: gzip-compressed bundled binary.
	gzPath := filepath.Join(explicit, "vpsagent-linux-amd64.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(elf); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// arm64: plain binary alongside .gz (plain must take precedence).
	plainPath := filepath.Join(explicit, "vpsagent-linux-arm64")
	if err := os.WriteFile(plainPath, elf, 0o755); err != nil {
		t.Fatal(err)
	}

	p.RegisterBundledAgent(explicit)

	wantVer := strings.TrimPrefix(version.Version, "v")
	if got := p.AgentCurrent(); got != wantVer {
		t.Fatalf("AgentCurrent() = %q, want %q", got, wantVer)
	}
	if path, ok := p.ResolveAgentFile("vpsagent-linux-amd64"); !ok || path != gzPath {
		t.Fatalf("ResolveAgentFile(amd64) = (%q, %v), want (%q, true)", path, ok, gzPath)
	}
	if path, ok := p.ResolveAgentFile("vpsagent-linux-arm64"); !ok || path != plainPath {
		t.Fatalf("ResolveAgentFile(arm64) = (%q, %v), want (%q, true)", path, ok, plainPath)
	}
	if path, ok := p.AgentReleaseFile(wantVer, "vpsagent-linux-amd64"); !ok || path != gzPath {
		t.Fatalf("AgentReleaseFile(amd64) = (%q, %v), want (%q, true)", path, ok, gzPath)
	}
}
