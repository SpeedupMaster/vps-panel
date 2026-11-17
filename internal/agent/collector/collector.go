// Package collector samples system metrics. The Linux implementation reads
// /proc directly to keep the agent small; other platforms return zeros so the
// agent can still be built and exercised during development.
package collector

import (
	"net"
	"strings"

	"vpspanel/internal/common/proto"
)

// NetCounters are cumulative per-interface byte counters.
type NetCounters struct {
	RX, TX uint64
}

// localIPs returns the preferred outbound IPv4/IPv6 source addresses.
func localIPs() (v4, v6 string) {
	if c, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		v4 = strings.Split(c.LocalAddr().String(), ":")[0]
		c.Close()
	}
	if c, err := net.Dial("udp6", "[2606:4700:4700::1111]:53"); err == nil {
		a := c.LocalAddr().(*net.UDPAddr)
		if !a.IP.IsPrivate() && !a.IP.IsLinkLocalUnicast() {
			v6 = a.IP.String()
		}
		c.Close()
	}
	return
}

// Collector keeps previous samples to compute rates.
type Collector struct {
	prevCPU  cpuTimes
	prevNet  NetCounters
	prevDisk [2]uint64
	prevTS   int64
	Iface    string // empty = all non-loopback interfaces
	DiskPath string
}

func New() *Collector { return &Collector{DiskPath: "/"} }

var _ = proto.Metrics{}
