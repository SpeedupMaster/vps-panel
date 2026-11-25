//go:build linux

package collector

import (
	"bufio"
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
	line := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(line)
	var t cpuTimes
	for i := 1; i < len(f); i++ {
		v, _ := strconv.ParseUint(f[i], 10, 64)
		if i <= 8 { // user..steal; guest is already in user
			t.total += v
		}
		switch i {
		case 4, 5: // idle, iowait
			t.idle += v
		case 8:
			t.steal = v
		}
	}
	return t
}

func meminfo() map[string]uint64 {
	m := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.Fields(v)[0], 10, 64)
		m[k] = n * 1024
	}
	return m
}

// ReadNet sums interface counters (excluding lo and virtual bridges).
func (c *Collector) ReadNet() NetCounters {
	var n NetCounters
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return n
	}
	for _, line := range strings.Split(string(b), "\n")[2:] {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if c.Iface != "" {
			if name != c.Iface {
				continue
			}
		} else if isExcludedIface(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		n.RX += rx
		n.TX += tx
	}
	return n
}

func readDisk() [2]uint64 {
	var out [2]uint64
	b, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		name := f[2]
		// whole disks only
		if isExcludedDisk(name) {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		out[0] += r * 512
		out[1] += w * 512
	}
	return out
}

func sockstat() (tcp, udp int) {
	for _, p := range []string{"/proc/net/sockstat", "/proc/net/sockstat6"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			n, _ := strconv.Atoi(f[2])
			switch f[0] {
			case "TCP:", "TCP6:":
				tcp += n
			case "UDP:", "UDP6:":
				udp += n
			}
		}
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
