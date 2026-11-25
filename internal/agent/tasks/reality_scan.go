package tasks

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

type RealityCandidate struct {
	Domain    string  `json:"domain"`
	OK        bool    `json:"ok"`
	TLS13     bool    `json:"tls13"`
	H2        bool    `json:"h2"`
	X25519    bool    `json:"x25519"`
	MLKEM768  bool    `json:"mlkem768,omitempty"`
	Redirect  string  `json:"redirect,omitempty"`
	Handshake float64 `json:"handshake_ms"`
	Error     string  `json:"error,omitempty"`
	// CDN in front of the target ("" = none found; the key is always sent so
	// the UI can tell results from agents without CDN detection apart).
	CDN   string `json:"cdn"`
	CDNBy string `json:"cdn_by,omitempty"`
}

// DefaultRealityCandidates are well-known sites that usually satisfy Reality's
// requirements; the agent tests them from the VPS itself.
var DefaultRealityCandidates = []string{
	"www.microsoft.com", "www.apple.com", "www.amazon.com", "www.cloudflare.com", "www.nvidia.com",
	"www.amd.com", "www.intel.com", "www.samsung.com", "www.tesla.com", "www.oracle.com",
	"dl.google.com", "www.yahoo.co.jp", "www.lovelive-anime.jp", "addons.mozilla.org", "www.speedtest.net",
}

func RealityScan(ctx context.Context, candidates string, out func(string)) []RealityCandidate {
	list := DefaultRealityCandidates
	if strings.TrimSpace(candidates) != "" {
		list = nil
		for _, d := range strings.FieldsFunc(candidates, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
			list = append(list, strings.TrimSpace(d))
		}
	}
	res := make([]RealityCandidate, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, d := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, d string) {
			defer wg.Done()
			defer func() { <-sem }()
			res[i] = scanOne(ctx, d)
		}(i, d)
	}
	wg.Wait()
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].OK != res[j].OK {
			return res[i].OK
		}
		return res[i].Handshake < res[j].Handshake
	})
	for _, r := range res {
		cdn := ""
		if r.CDN != "" {
			cdn = " CDN:" + r.CDN + "（" + r.CDNBy + "）"
		}
		pq := ""
		if r.MLKEM768 {
			pq = " mlkem768=true"
		}
		out(fmt.Sprintf("%-26s ok=%-5v tls13=%-5v h2=%-5v x25519=%-5v%s %6.1fms %s%s%s\n", r.Domain, r.OK, r.TLS13, r.H2, r.X25519, pq, r.Handshake, r.Redirect, r.Error, cdn))
	}
	return res
}

var (
	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")
	net0Prefix  = netip.MustParsePrefix("0.0.0.0/8")

	// Overridable in tests.
	lookupScanIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
)

// isBlockedScanIP reports whether ip falls into loopback, RFC1918, link-local,
// CGNAT, IPv6 ULA/link-local, multicast, or unspecified space.
func isBlockedScanIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	if addr.Is4() && (cgnatPrefix.Contains(addr) || net0Prefix.Contains(addr)) {
		return true
	}
	return false
}

func resolvePublicScanIP(ctx context.Context, domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" || strings.ContainsAny(domain, `/\@?#: `) {
		return "", fmt.Errorf("非法域名：%q", domain)
	}
	if ip := net.ParseIP(domain); ip != nil {
		if isBlockedScanIP(ip) {
			return "", errors.New("拒绝扫描内网或保留地址")
		}
		return ip.String(), nil
	}
	addrs, err := lookupScanIPs(ctx, domain)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", errors.New("域名解析无结果")
	}
	for _, a := range addrs {
		if isBlockedScanIP(a.IP) {
			return "", errors.New("拒绝扫描解析到内网或保留地址的域名")
		}
	}
	return addrs[0].IP.String(), nil
}

func scanOne(ctx context.Context, domain string) RealityCandidate {
	r := RealityCandidate{Domain: domain}
	targetIP, err := resolvePublicScanIP(ctx, domain)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	cfg := &tls.Config{
		ServerName:       domain,
		MinVersion:       tls.VersionTLS13,
		NextProtos:       []string{"h2", "http/1.1"},
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519},
	}
	start := time.Now()
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(targetIP, "443"))
	if err != nil {
		r.Error = err.Error()
		return r
	}
	conn := tls.Client(raw, cfg)
	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		r.Error = "握手失败：" + err.Error()
		return r
	}
	r.Handshake = float64(time.Since(start).Microseconds()) / 1000
	st := conn.ConnectionState()
	r.TLS13 = st.Version == tls.VersionTLS13
	r.H2 = st.NegotiatedProtocol == "h2"
	r.X25519 = true // handshake only offered X25519 / X25519MLKEM768
	r.MLKEM768 = st.CurveID == tls.X25519MLKEM768
	conn.Close()

	// Pin the validated public IP for the HTTP check too so DNS rebinding cannot
	// redirect it to an internal address.
	pinnedTr := &http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, perr := net.SplitHostPort(addr)
			if perr != nil {
				port = "443"
			}
			return d.DialContext(ctx, network, net.JoinHostPort(targetIP, port))
		},
	}
	defer pinnedTr.CloseIdleConnections()
	pinnedClient := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     pinnedTr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var resp *http.Response
	if req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil); rerr == nil {
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err = pinnedClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	} else {
		err = rerr
	}
	if err == nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if u, perr := http.NewRequest("GET", loc, nil); perr == nil && u.URL.Host != "" && u.URL.Host != domain {
			r.Redirect = "跳转到 " + u.URL.Host
		}
	}
	var hdr http.Header
	if err == nil {
		hdr = resp.Header
	}
	r.CDN, r.CDNBy = detectCDN(ctx, domain, hdr)
	r.OK = r.TLS13 && r.H2 && r.X25519 && r.Redirect == "" && r.CDN == ""
	return r
}
