package main

import (
	"os"
	"path/filepath"
)

// bundledAgentDir locates pre-built agent binaries shipped beside the panel
// binary (or in the explicit directory from VPSPANEL_AGENT_DIR).
func bundledAgentDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	cand := filepath.Join(filepath.Dir(exe), "agent")
	if info, err := os.Stat(cand); err == nil && info.IsDir() {
		return cand
	}
	return ""
}
