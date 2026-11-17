// Package config reads /etc/vpsagent/agent.yaml (local safety switches)
// and credentials.json (written by `vpsagent register`).
package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"vpspanel/internal/common/proto"
)

var (
	Dir     = "/etc/vpsagent"
	DataDir = "/var/lib/vpsagent"
)

type Local struct {
	Mode     string
	Switches proto.LocalSwitches
}

// LoadLocal parses the flat key: value file. Unknown keys are ignored.
func LoadLocal() Local {
	l := Local{Mode: "probe", Switches: proto.LocalSwitches{AllowSystemTune: true, AllowSelfUpdate: true}}
	f, err := os.Open(filepath.Join(Dir, "agent.yaml"))
	if err != nil {
		return l
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		b := v == "true" || v == "yes" || v == "1"
		switch k {
		case "mode":
			if v == "node" || v == "probe" {
				l.Mode = v
			}
		case "allow_remote_exec":
			l.Switches.AllowRemoteExec = b
		case "allow_system_tune":
			l.Switches.AllowSystemTune = b
		case "allow_self_update":
			l.Switches.AllowSelfUpdate = b
		}
	}
	return l
}

// SetMode rewrites the mode line (used when the panel upgrades a probe to a
// node; the mode itself is not a safety switch).
func SetMode(mode string) error {
	path := filepath.Join(Dir, "agent.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "mode:") {
			lines[i] = "mode: " + mode + "                # probe | node"
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

type Credentials struct {
	PanelURLs   []string `json:"panel_urls"`
	AgentID     string   `json:"agent_id"`
	AgentSecret string   `json:"agent_secret"`
	ServerName  string   `json:"server_name"`
}

func credPath() string { return filepath.Join(Dir, "credentials.json") }

func LoadCredentials() (*Credentials, error) {
	b, err := os.ReadFile(credPath())
	if err != nil {
		return nil, err
	}
	var c Credentials
	return &c, json.Unmarshal(b, &c)
}

func SaveCredentials(c *Credentials) error {
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := credPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, credPath())
}
