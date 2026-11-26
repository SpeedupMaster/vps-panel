package main

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	defaultPanelMemLimit int64 = 64 << 20
	// cgroup v1 reports ~2^63-4096 when unlimited; anything above 1 TiB is treated as uncapped.
	maxReasonableCgroupLimit int64 = 1 << 40
)

// tuneMemory keeps the panel's heap compact in containers and small VPSes.
// Explicit GOGC / GOMEMLIMIT environment variables always take precedence.
func tuneMemory() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		limit, ok := readCgroupMemoryLimit()
		debug.SetMemoryLimit(computeMemoryLimit(limit, ok))
	}
}

func readCgroupMemoryLimit() (int64, bool) {
	for _, p := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if n, ok := parseCgroupLimit(string(b)); ok {
			return n, true
		}
	}
	return 0, false
}

func parseCgroupLimit(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "max" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= maxReasonableCgroupLimit {
		return 0, false
	}
	return n, true
}

func computeMemoryLimit(cgroupBytes int64, hasCgroup bool) int64 {
	if !hasCgroup || cgroupBytes <= 0 {
		return defaultPanelMemLimit
	}
	return cgroupBytes * 4 / 5
}
