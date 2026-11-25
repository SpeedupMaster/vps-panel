package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadLocalDefaultsAndParsing(t *testing.T) {
	oldDir := Dir
	defer func() { Dir = oldDir }()
	Dir = t.TempDir()

	// Missing agent.yaml falls back to safe defaults.
	def := LoadLocal()
	if def.Mode != "probe" || def.Switches.AllowRemoteExec || !def.Switches.AllowSystemTune || !def.Switches.AllowSelfUpdate {
		t.Fatalf("unexpected defaults: %+v", def)
	}

	raw := `
# comment line
mode: "node" # inline comment
allow_remote_exec: yes
allow_system_tune: false
allow_self_update: 1
unknown_key: ignored
`
	if err := os.WriteFile(filepath.Join(Dir, "agent.yaml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got := LoadLocal()
	if got.Mode != "node" || !got.Switches.AllowRemoteExec || got.Switches.AllowSystemTune || !got.Switches.AllowSelfUpdate {
		t.Fatalf("parsed Local = %+v", got)
	}

	// Invalid mode value keeps default "probe".
	if err := os.WriteFile(filepath.Join(Dir, "agent.yaml"), []byte("mode: invalid\nallow_self_update: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = LoadLocal()
	if got.Mode != "probe" || got.Switches.AllowSelfUpdate {
		t.Fatalf("invalid mode Local = %+v", got)
	}
}

func TestSetModePreservesSwitches(t *testing.T) {
	oldDir := Dir
	defer func() { Dir = oldDir }()
	Dir = t.TempDir()

	orig := "mode: probe                # probe | node\nallow_remote_exec: true\nallow_system_tune: false\n"
	if err := os.WriteFile(filepath.Join(Dir, "agent.yaml"), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMode("node"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	got := LoadLocal()
	if got.Mode != "node" || !got.Switches.AllowRemoteExec || got.Switches.AllowSystemTune {
		t.Fatalf("after SetMode: %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(Dir, "agent.yaml"))
	if !strings.Contains(string(b), "mode: node") {
		t.Fatalf("agent.yaml missing updated mode:\n%s", b)
	}
}

func TestSaveAndLoadCredentialsAtomic(t *testing.T) {
	oldDir := Dir
	defer func() { Dir = oldDir }()
	Dir = filepath.Join(t.TempDir(), "nested-conf")

	want := &Credentials{
		PanelURLs:   []string{"https://panel.example.com", "https://backup.example.com"},
		AgentID:     "agent-123",
		AgentSecret: "super-secret",
		ServerName:  "hk-01",
	}
	if err := SaveCredentials(want); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	got, err := LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadCredentials = %+v, want %+v", got, want)
	}
	// Ensure atomic rename left no temp file behind.
	entries, err := os.ReadDir(Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "credentials.json" {
		t.Fatalf("unexpected files in %s: %v", Dir, entries)
	}
}
