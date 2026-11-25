package tasks

import (
	"context"
	"net"
	"reflect"
	"testing"

	"vpspanel/internal/agent"
	"vpspanel/internal/agent/config"
	"vpspanel/internal/common/proto"
)

func TestParseHopsSkipsHeader(t *testing.T) {
	text := `traceroute to sh-ct-v4.ip.zstaticcdn.com (101.226.101.195), 25 hops max, 60 byte packets
 1  172.30.208.1  0.250 ms
 2  192.168.1.1  0.386 ms
 3  *
 4  202.97.120.213  25.302 ms
10  101.95.219.70  27.659 ms
`
	got := parseHops(text)
	want := []string{"172.30.208.1", "192.168.1.1", "202.97.120.213", "101.95.219.70"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGeminiVerdict(t *testing.T) {
	cases := []struct{ body, status, region string }{
		{`x,2,1,200,"USA"y "45631641\":true,\"45841483\":true`, "yes", "US"},
		{`x,2,1,200,"JPN"y [45631641,null,true]`, "yes", "JP"},
		{`x,2,1,200,"HKG"y "45631641\":false`, "no", "HK"},
		{`x,2,1,200,"CHN"y no flag here`, "no", "CN"},
		{`x,2,1,200,"DEU"y no flag here`, "yes", "DE"},
		{`unrelated page`, "unknown", ""},
	}
	for _, c := range cases {
		s, r, _ := geminiVerdict(c.body)
		if s != c.status || r != c.region {
			t.Errorf("%q: got %s/%s want %s/%s", c.body, s, r, c.status, c.region)
		}
	}
}

func TestParseTraceHost(t *testing.T) {
	good := map[string]string{
		"sh-ct-v4.ip.zstaticcdn.com":     "sh-ct-v4.ip.zstaticcdn.com",
		"sh-ct-v4.ip.zstaticcdn.com:443": "sh-ct-v4.ip.zstaticcdn.com",
		"1.2.3.4":                        "1.2.3.4",
		"1.2.3.4:80":                     "1.2.3.4",
		"2001:db8::1":                    "2001:db8::1",
		"[2001:db8::1]:443":              "2001:db8::1",
		"[2001:db8::1]":                  "2001:db8::1",
	}
	for in, want := range good {
		got, err := parseTraceHost(in)
		if err != nil || got != want {
			t.Errorf("parseTraceHost(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
	bad := []string{"", "   ", "-f", "--help", "-n:80", "a;rm -rf /", "a b", "foo..bar", "http://example.com"}
	for _, in := range bad {
		if got, err := parseTraceHost(in); err == nil {
			t.Errorf("parseTraceHost(%q) unexpectedly succeeded with %q", in, got)
		}
	}
}

func TestRealityScanRejectsPrivateAndReservedIPs(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.0.0.2", "::1",
		"10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "fe80::1",
		"100.64.0.1", "100.127.255.254",
		"fc00::1", "fd12:3456::1",
		"0.0.0.0", "::",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.5",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if !isBlockedScanIP(ip) {
			t.Errorf("isBlockedScanIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if isBlockedScanIP(ip) {
			t.Errorf("isBlockedScanIP(%s) = true, want false", s)
		}
	}

	oldLookup := lookupScanIPs
	defer func() { lookupScanIPs = oldLookup }()
	lookupScanIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "imds.evil.test":
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		case "mixed.evil.test":
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		case "ula.evil.test":
			return []net.IPAddr{{IP: net.ParseIP("fd00::1")}}, nil
		default:
			return []net.IPAddr{{IP: net.ParseIP("100.64.1.2")}}, nil
		}
	}
	for _, host := range []string{"imds.evil.test", "mixed.evil.test", "ula.evil.test", "cgnat.evil.test", "127.0.0.1"} {
		res := scanOne(context.Background(), host)
		if res.OK || res.Error == "" {
			t.Errorf("scanOne(%q) should have been rejected: %+v", host, res)
		}
	}
}

func TestInstallXrayBlockedOnProbeModeWithoutSwitches(t *testing.T) {
	a := agent.New(&config.Credentials{PanelURLs: []string{"https://panel.example.com"}}, config.Local{
		Mode:     "probe",
		Switches: proto.LocalSwitches{AllowSelfUpdate: false, AllowRemoteExec: false},
	}, nil)
	Register(a)

	fn := a.Tasks[proto.TaskInstallXray]
	if _, err := fn(context.Background(), &proto.Task{
		Type: proto.TaskInstallXray,
		Args: map[string]string{"base_url": "https://evil.example.com/xray"},
	}, func(string) {}); err == nil {
		t.Fatal("expected install_xray to be blocked on probe mode when switches are off")
	}

	// When allow_self_update is enabled, the probe gate passes and base_url validation rejects untrusted hosts.
	a.Local.Switches.AllowSelfUpdate = true
	if _, err := fn(context.Background(), &proto.Task{
		Type: proto.TaskInstallXray,
		Args: map[string]string{"version": "26.1.1", "base_url": "https://evil.example.com/xray"},
	}, func(string) {}); err == nil {
		t.Fatal("expected untrusted base_url to be rejected")
	}
}
