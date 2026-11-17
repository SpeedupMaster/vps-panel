package xraymgr

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
)

const clkTck = 100 // USER_HZ on all mainstream Linux builds

type procSampler struct {
	mu       sync.Mutex
	pid      int
	lastTick uint64
	lastAt   time.Time
}

// sample reads /proc/<pid>/stat and status for CPU %, RSS and uptime.
func (s *procSampler) sample() *proto.XrayProc {
	p := &proto.XrayProc{}
	pid := mainPID()
	if pid <= 0 {
		return p
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return p
	}
	// fields after the ")" of comm
	str := string(stat)
	if i := strings.LastIndexByte(str, ')'); i >= 0 {
		str = str[i+2:]
	}
	f := strings.Fields(str)
	if len(f) < 20 {
		return p
	}
	p.Running, p.PID = true, pid
	utime, _ := strconv.ParseUint(f[11], 10, 64)
	stime, _ := strconv.ParseUint(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	ticks := utime + stime

	s.mu.Lock()
	now := time.Now()
	if s.pid == pid && !s.lastAt.IsZero() {
		dt := now.Sub(s.lastAt).Seconds()
		if dt > 0 && ticks >= s.lastTick {
			p.CPU = float64(ticks-s.lastTick) / clkTck / dt * 100
			p.CPU = float64(int(p.CPU*10)) / 10
		}
	}
	s.pid, s.lastTick, s.lastAt = pid, ticks, now
	s.mu.Unlock()

	if up, err := os.ReadFile("/proc/uptime"); err == nil {
		if sys, err := strconv.ParseFloat(strings.Fields(string(up))[0], 64); err == nil {
			p.Uptime = int64(sys - float64(start)/clkTck)
		}
	}
	if st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
		for _, l := range strings.Split(string(st), "\n") {
			if v, ok := strings.CutPrefix(l, "VmRSS:"); ok {
				kb, _ := strconv.ParseUint(strings.Fields(v)[0], 10, 64)
				p.Mem = kb * 1024
			}
		}
	}
	return p
}
