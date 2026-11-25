// Package collector samples system metrics. The Linux implementation reads
// /proc directly to keep the agent small; other platforms return zeros so the
// agent can still be built and exercised during development.
package collector

import (
	"net"
	"net/netip"
	"regexp"
	"strings"
	"sync"

	"vpspanel/internal/common/proto"
)

// NetCounters are cumulative per-interface byte counters.
type NetCounters struct {
	RX, TX uint64
}

// cgnat is the carrier-grade NAT range (100.64.0.0/10): not public either.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// partition matches sda1 / vdb2 / xvda1 / nvme0n1p1 / mmcblk0p1.
var partition = regexp.MustCompile(`^((s|v|xv|h)d[a-z]+\d+|(nvme\d+n\d+|mmcblk\d+)p\d+)$`)

func subClamp(total, avail uint64) uint64 {
	if avail >= total {
		return 0
	}
	return total - avail
}

// isExcludedIface filters loopback, container bridges, and tunnel interfaces
// so traffic isn't double-counted across overlay/VPN devices.
func isExcludedIface(name string) bool {
	if name == "lo" || name == "CloudflareWARP" || name == "he-ipv6" {
		return true
	}
	for _, p := range []string{
		"docker", "veth", "br-", "virbr", "tun", "wg",
		"warp", "tailscale", "zt", "podman", "cni", "cali", "sit", "ip6tnl",
	} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isExcludedDisk filters virtual/mapped/RAID/compressed devices and partitions.
func isExcludedDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "dm-", "zram", "md"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return partition.MatchString(name)
}

// parseCPUModel extracts a human-readable CPU model from /proc/cpuinfo,
// falling back to ARM64 "Model" / "Processor" / "Hardware" keys.
func parseCPUModel(cpuinfo string) string {
	var fallbackModel, fallbackProc, fallbackHW string
	for _, l := range strings.Split(cpuinfo, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch k {
		case "model name":
			return v
		case "Model":
			if fallbackModel == "" {
				fallbackModel = v
			}
		case "Processor":
			if fallbackProc == "" {
				fallbackProc = v
			}
		case "Hardware":
			if fallbackHW == "" {
				fallbackHW = v
			}
		}
	}
	if fallbackModel != "" {
		return fallbackModel
	}
	if fallbackProc != "" {
		return fallbackProc
	}
	return fallbackHW
}

func isPublicIPv4(s string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !a.Is4() {
		return false
	}
	return !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() && !a.IsUnspecified() && !a.IsMulticast() && !cgnat.Contains(a)
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

// CurrentIPs returns the host's public IPv4 (falling back to HTTP echo on NAT
// VPSes) and global IPv6 address.
func (c *Collector) CurrentIPs() (v4, v6 string) {
	v4, v6 = localIPs()
	if !isPublicIPv4(v4) {
		v4 = PublicIPv4()
	}
	return v4, v6
}

// Collector keeps previous samples to compute rates.
type Collector struct {
	mu       sync.Mutex
	prevCPU  cpuTimes
	prevNet  NetCounters
	prevDisk [2]uint64
	prevTS   int64
	Iface    string // empty = all non-loopback interfaces
	DiskPath string
}

func New() *Collector { return &Collector{DiskPath: "/"} }

var _ = proto.Metrics{}
