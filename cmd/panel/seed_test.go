package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedAgentsSyncBehavior(t *testing.T) {
	dataDir := t.TempDir()
	srcDir := t.TempDir()

	srcBin := filepath.Join(srcDir, "vpsagent-linux-amd64")
	dstBin := filepath.Join(dataDir, "agent", "vpsagent-linux-amd64")
	dstSig := dstBin + ".sig"

	if err := os.WriteFile(srcBin, []byte("agent-v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Initial seed populates missing binaries.
	seedAgents(dataDir, srcDir, true)
	got, err := os.ReadFile(dstBin)
	if err != nil || string(got) != "agent-v1" {
		t.Fatalf("after initial seed: got %q, err %v", got, err)
	}

	// Place a stale signature and update bundled binary.
	if err := os.WriteFile(dstSig, []byte("old-sig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcBin, []byte("agent-v2-newer"), 0o755); err != nil {
		t.Fatal(err)
	}

	// When a custom release is manually published (overwrite=false), keep existing file.
	seedAgents(dataDir, srcDir, false)
	got, err = os.ReadFile(dstBin)
	if err != nil || string(got) != "agent-v1" {
		t.Fatalf("overwrite=false should keep existing binary, got %q, err %v", got, err)
	}

	// When no custom release is published (overwrite=true), sync the new bundled binary
	// and remove any stale .sig file not present in src.
	seedAgents(dataDir, srcDir, true)
	got, err = os.ReadFile(dstBin)
	if err != nil || string(got) != "agent-v2-newer" {
		t.Fatalf("overwrite=true should update binary, got %q, err %v", got, err)
	}
	if _, err := os.Stat(dstSig); !os.IsNotExist(err) {
		t.Fatalf("expected stale .sig to be removed, stat err=%v", err)
	}
}
