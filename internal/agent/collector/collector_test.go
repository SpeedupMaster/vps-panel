package collector

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSubClampPreventsUnderflow(t *testing.T) {
	if got := subClamp(100, 250); got != 0 {
		t.Fatalf("subClamp(100, 250) = %d, want 0", got)
	}
	if got := subClamp(250, 100); got != 150 {
		t.Fatalf("subClamp(250, 100) = %d, want 150", got)
	}
	if got := subClamp(0, 0); got != 0 {
		t.Fatalf("subClamp(0, 0) = %d, want 0", got)
	}
}

func TestExcludedIfacesAndDisks(t *testing.T) {
	excludedIfaces := []string{
		"lo", "docker0", "veth1234", "br-abcd", "virbr0", "tun0", "wg0",
		"warp0", "CloudflareWARP", "tailscale0", "zt01234", "podman0",
		"cni0", "cali1234", "sit0", "ip6tnl0", "he-ipv6",
	}
	for _, name := range excludedIfaces {
		if !isExcludedIface(name) {
			t.Errorf("isExcludedIface(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"eth0", "ens3", "enp1s0", "eno1", "bond0"} {
		if isExcludedIface(name) {
			t.Errorf("isExcludedIface(%q) = true, want false", name)
		}
	}

	excludedDisks := []string{"loop0", "ram0", "dm-0", "zram0", "md0", "md127", "sda1", "vda2", "nvme0n1p1", "mmcblk0p2"}
	for _, name := range excludedDisks {
		if !isExcludedDisk(name) {
			t.Errorf("isExcludedDisk(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"sda", "vda", "xvda", "nvme0n1", "mmcblk0"} {
		if isExcludedDisk(name) {
			t.Errorf("isExcludedDisk(%q) = true, want false", name)
		}
	}
}

func TestParseCPUModelARM64Fallback(t *testing.T) {
	x86 := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Xeon(R) E5-2680\n"
	if got := parseCPUModel(x86); got != "Intel(R) Xeon(R) E5-2680" {
		t.Fatalf("x86 cpu model = %q", got)
	}
	armModel := "processor\t: 0\nBogoMIPS\t: 108.00\nModel\t\t: Raspberry Pi 4 Model B Rev 1.5\n"
	if got := parseCPUModel(armModel); got != "Raspberry Pi 4 Model B Rev 1.5" {
		t.Fatalf("arm Model = %q", got)
	}
	armProc := "Processor\t: ARMv8 Processor rev 4 (v8l)\nprocessor\t: 0\n"
	if got := parseCPUModel(armProc); got != "ARMv8 Processor rev 4 (v8l)" {
		t.Fatalf("arm Processor = %q", got)
	}
}

func TestPublicIPv4Fallback(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("10.0.0.1\n")) // private IP must be rejected
	}))
	defer s2.Close()
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.42\n"))
	}))
	defer s3.Close()

	oldEndpoints := publicIPEndpoints
	defer func() { publicIPEndpoints = oldEndpoints }()
	publicIPEndpoints = []ipEndpoint{
		{url: s1.URL, trace: true},
		{url: s2.URL},
		{url: s3.URL},
	}

	if got := PublicIPv4(); got != "203.0.113.42" {
		t.Fatalf("PublicIPv4() = %q, want 203.0.113.42", got)
	}
}

func TestSampleConcurrent(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if m := c.Sample(); m == nil || m.TS == 0 {
					t.Errorf("invalid metrics sample: %+v", m)
				}
			}
		}()
	}
	wg.Wait()
}

func TestProcParsers(t *testing.T) {
	cpu := parseCPUStat("cpu  100 10 50 800 20 5 5 10 0 0\ncpu0 50 5 25 400 10 2 3 5 0 0\n")
	if cpu.total != 1000 || cpu.idle != 820 || cpu.steal != 10 {
		t.Fatalf("parseCPUStat = %+v, want {total:1000 idle:820 steal:10}", cpu)
	}

	mi := parseMeminfo("MemTotal:        1048576 kB\nMemAvailable:    2097152 kB\nSwapTotal:        524288 kB\nSwapFree:         262144 kB\n")
	if got := subClamp(mi["MemTotal"], mi["MemAvailable"]); got != 0 {
		t.Fatalf("LXC MemAvailable > MemTotal underflowed: %d", got)
	}
	if got := subClamp(mi["SwapTotal"], mi["SwapFree"]); got != 262144*1024 {
		t.Fatalf("SwapUsed = %d, want %d", got, 262144*1024)
	}

	netDev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 99999 10 0 0 0 0 0 0 99999 10 0 0 0 0 0 0
  eth0: 1000 10 0 0 0 0 0 0 2000 10 0 0 0 0 0 0
 warp0: 5000 10 0 0 0 0 0 0 5000 10 0 0 0 0 0 0
CloudflareWARP: 5000 10 0 0 0 0 0 0 5000 10 0 0 0 0 0 0
tailscale0: 5000 10 0 0 0 0 0 0 5000 10 0 0 0 0 0 0
  ens3:  500 10 0 0 0 0 0 0  700 10 0 0 0 0 0 0
`
	if got := parseNetDev(netDev, ""); got.RX != 1500 || got.TX != 2700 {
		t.Fatalf("parseNetDev(all) = %+v, want {RX:1500 TX:2700}", got)
	}
	if got := parseNetDev(netDev, "eth0"); got.RX != 1000 || got.TX != 2000 {
		t.Fatalf("parseNetDev(eth0) = %+v, want {RX:1000 TX:2000}", got)
	}

	diskstats := `   8       0 sda 1 0 10 0 1 0 20 0 0 0 0
   8       1 sda1 1 0 10 0 1 0 20 0 0 0 0
 252       0 zram0 1 0 999 0 1 0 999 0 0 0 0
   9       0 md0 1 0 999 0 1 0 999 0 0 0 0
 259       0 nvme0n1 1 0 4 0 1 0 6 0 0 0 0
 259       1 nvme0n1p1 1 0 4 0 1 0 6 0 0 0 0
`
	if got := parseDiskstats(diskstats); got[0] != 14*512 || got[1] != 26*512 {
		t.Fatalf("parseDiskstats = %v, want [%d %d]", got, 14*512, 26*512)
	}

	tcp, udp := parseSockstat("sockets: used 100\nTCP: inuse 12 orphan 0\nUDP: inuse 4 mem 0\nTCP6: inuse 3\nUDP6: inuse 2\n")
	if tcp != 15 || udp != 6 {
		t.Fatalf("parseSockstat = (%d, %d), want (15, 6)", tcp, udp)
	}
}
