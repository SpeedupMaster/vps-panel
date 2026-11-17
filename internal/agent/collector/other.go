//go:build !linux

package collector

import (
	"os"
	"runtime"
	"time"

	"vpspanel/internal/common/proto"
)

type cpuTimes struct{ total, idle, steal uint64 }

func (c *Collector) ReadNet() NetCounters { return NetCounters{} }

func (c *Collector) Sample() *proto.Metrics { return &proto.Metrics{TS: time.Now().Unix()} }

func DetectVirt() string { return "unknown" }

func (c *Collector) System() proto.SystemInfo {
	h, _ := os.Hostname()
	v4, v6 := localIPs()
	return proto.SystemInfo{Hostname: h, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUCores: runtime.NumCPU(), IPv4: v4, IPv6: v6, Virt: "unknown"}
}
