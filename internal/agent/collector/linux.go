//go:build linux

package collector

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vpspanel/internal/common/proto"
)

type cpuTimes struct{ total, idle, steal uint64 }

func readCPU() cpuTimes {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}
	}
	return parseCPUStat(string(b))
}

func meminfo() map[string]uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return map[string]uint64{}
	}
	return parseMeminfo(string(b))
}

// ReadNet sums interface counters (excluding lo and virtual bridges).
func (c *Collector) ReadNet() NetCounters {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return NetCounters{}
	}
	return parseNetDev(string(b), c.Iface)
}

func readDisk() [2]uint64 {
	b, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return [2]uint64{}
	}
	return parseDiskstats(string(b))
}

func sockstat() (tcp, udp int) {
	for _, p := range []string{"/proc/net/sockstat", "/proc/net/sockstat6"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		t, u := parseSockstat(string(b))
		tcp += t
		udp += u
	}
	return
}

func uptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	return int64(f)
}

// Sample returns one metrics frame; rates are relative to the previous call.
func (c *Collector) Sample() *proto.Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	m := &proto.Metrics{TS: now, Uptime: uptime()}
	cpu := readCPU()
	if c.prevCPU.total > 0 && cpu.total > c.prevCPU.total {
		dt := float64(cpu.total - c.prevCPU.total)
		m.CPU = 100 * (1 - float64(subClamp(cpu.idle, c.prevCPU.idle))/dt)
		m.Steal = 100 * float64(subClamp(cpu.steal, c.prevCPU.steal)) / dt
	}
	c.prevCPU = cpu

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			m.Load1, _ = strconv.ParseFloat(f[0], 64)
			m.Load5, _ = strconv.ParseFloat(f[1], 64)
			m.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	mi := meminfo()
	m.MemUsed = subClamp(mi["MemTotal"], mi["MemAvailable"])
	m.SwapUsed = subClamp(mi["SwapTotal"], mi["SwapFree"])

	var fs syscall.Statfs_t
	if syscall.Statfs(c.DiskPath, &fs) == nil {
		m.DiskUsed = subClamp(fs.Blocks, fs.Bfree) * uint64(fs.Bsize)
	}
	net := c.ReadNet()
	disk := readDisk()
	if c.prevTS > 0 && now > c.prevTS {
		dt := uint64(now - c.prevTS)
		if net.RX >= c.prevNet.RX && net.TX >= c.prevNet.TX {
			m.NetIn = (net.RX - c.prevNet.RX) / dt
			m.NetOut = (net.TX - c.prevNet.TX) / dt
		}
		if disk[0] >= c.prevDisk[0] && disk[1] >= c.prevDisk[1] {
			m.DiskRead = (disk[0] - c.prevDisk[0]) / dt
			m.DiskWrite = (disk[1] - c.prevDisk[1]) / dt
		}
	}
	c.prevNet, c.prevDisk, c.prevTS = net, disk, now
	m.TCPConns, m.UDPConns = sockstat()
	return m
}

func readFirst(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

// DetectVirt returns kvm / openvz / lxc / docker / xen / none ...
func DetectVirt() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "systemd-detect-virt").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if _, err := os.Stat("/proc/user_beancounters"); err == nil {
		return "openvz"
	}
	if b, _ := os.ReadFile("/proc/1/environ"); strings.Contains(string(b), "container=lxc") {
		return "lxc"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if strings.Contains(readFirst("/proc/cpuinfo"), "hypervisor") {
		return "vm"
	}
	return "none"
}

// System gathers static host information for Hello.
func (c *Collector) System() proto.SystemInfo {
	s := proto.SystemInfo{Arch: runtime.GOARCH, CPUCores: runtime.NumCPU()}
	s.Hostname, _ = os.Hostname()
	s.Kernel = readFirst("/proc/sys/kernel/osrelease")
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
				s.OS = strings.Trim(v, `"`)
			}
		}
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		s.CPUModel = parseCPUModel(string(b))
	}
	mi := meminfo()
	s.MemTotal, s.SwapTotal = mi["MemTotal"], mi["SwapTotal"]
	var fs syscall.Statfs_t
	if syscall.Statfs(c.DiskPath, &fs) == nil {
		s.DiskTotal = fs.Blocks * uint64(fs.Bsize)
	}
	s.BootTime = time.Now().Unix() - uptime()
	s.Virt = DetectVirt()
	s.IPv4, s.IPv6 = c.CurrentIPs()
	s.Congestion = readFirst("/proc/sys/net/ipv4/tcp_congestion_control")
	s.Qdisc = readFirst("/proc/sys/net/core/default_qdisc")
	s.AvailCC = readFirst("/proc/sys/net/ipv4/tcp_available_congestion_control")
	return s
}
