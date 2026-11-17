package tasks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

func httpClient(timeout time.Duration, follow bool) *http.Client {
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true, ForceAttemptHTTP2: true}}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"

func get(ctx context.Context, url string, follow bool) (*http.Response, string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := httpClient(15*time.Second, follow).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp, string(b), nil
}

// ---------- streaming / AI unlock ----------

type UnlockItem struct {
	Name   string `json:"name"`
	Status string `json:"status"` // yes | partial | no | unknown
	Region string `json:"region,omitempty"`
	Detail string `json:"detail,omitempty"`
}

var (
	reCountry  = regexp.MustCompile(`"(?:countryCode|GL|region)":"([A-Z]{2})"`)
	reNFRegion = regexp.MustCompile(`netflix\.com/([a-z]{2})(?:-[a-z]{2})?/`)
)

func Unlock(ctx context.Context, out func(string)) []UnlockItem {
	checks := []func(context.Context) UnlockItem{checkNetflix, checkYouTube, checkChatGPT, checkClaude, checkGemini, checkDisney, checkTikTok}
	res := make([]UnlockItem, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c func(context.Context) UnlockItem) {
			defer wg.Done()
			res[i] = c(ctx)
		}(i, c)
	}
	wg.Wait()
	for _, r := range res {
		out(fmt.Sprintf("%-16s %-8s %s %s\n", r.Name, r.Status, r.Region, r.Detail))
	}
	return res
}

func checkNetflix(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "Netflix", Status: "unknown"}
	r1, _, err1 := get(ctx, "https://www.netflix.com/title/81280792", false) // original
	r2, _, err2 := get(ctx, "https://www.netflix.com/title/70143836", false) // licensed
	if err1 != nil || err2 != nil {
		it.Detail = "请求失败"
		return it
	}
	okCode := func(r *http.Response) bool { return r.StatusCode == 200 || (r.StatusCode >= 300 && r.StatusCode < 400) }
	switch {
	case r1.StatusCode == 403:
		it.Status, it.Detail = "no", "IP 被封锁"
	case okCode(r1) && okCode(r2) && r2.StatusCode != 404:
		it.Status, it.Detail = "yes", "完整解锁"
	case okCode(r1):
		it.Status, it.Detail = "partial", "仅自制剧"
	default:
		it.Status = "no"
	}
	if loc := r1.Header.Get("Location"); loc != "" {
		if m := reNFRegion.FindStringSubmatch(loc); m != nil {
			it.Region = strings.ToUpper(m[1])
		}
	}
	if it.Region == "" && it.Status != "no" {
		it.Region = "US"
	}
	return it
}

func checkYouTube(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "YouTube Premium", Status: "unknown"}
	_, body, err := get(ctx, "https://www.youtube.com/premium", true)
	if err != nil {
		return it
	}
	if strings.Contains(body, "Premium is not available in your country") {
		it.Status = "no"
		return it
	}
	it.Status = "yes"
	if m := regexp.MustCompile(`"INNERTUBE_CONTEXT_GL":"([A-Z]{2})"`).FindStringSubmatch(body); m != nil {
		it.Region = m[1]
	} else if m := reCountry.FindStringSubmatch(body); m != nil {
		it.Region = m[1]
	}
	return it
}

func cfTraceLoc(ctx context.Context, url string) string {
	_, body, err := get(ctx, url, true)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(l, "loc="); ok {
			return v
		}
	}
	return ""
}

func checkChatGPT(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "ChatGPT", Status: "unknown"}
	it.Region = cfTraceLoc(ctx, "https://chatgpt.com/cdn-cgi/trace")
	_, body, err := get(ctx, "https://api.openai.com/compliance/cookie_requirements", true)
	if err != nil {
		return it
	}
	_, body2, _ := get(ctx, "https://ios.chat.openai.com/", true)
	switch {
	case strings.Contains(body, "unsupported_country"):
		it.Status, it.Detail = "no", "地区不支持"
	case strings.Contains(body2, "VPN"):
		it.Status, it.Detail = "partial", "网页可用，App 被识别为 VPN"
	default:
		it.Status = "yes"
	}
	return it
}

func checkClaude(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "Claude", Status: "unknown"}
	resp, _, err := get(ctx, "https://claude.ai/", false)
	if err != nil {
		return it
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "unavailable") {
		it.Status, it.Detail = "no", "地区不支持"
	} else {
		it.Status = "yes"
	}
	it.Region = cfTraceLoc(ctx, "https://claude.ai/cdn-cgi/trace")
	return it
}

func checkGemini(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "Gemini", Status: "unknown"}
	_, body, err := get(ctx, "https://gemini.google.com/", true)
	if err != nil {
		return it
	}
	if strings.Contains(body, "45631641,null,true") {
		it.Status = "yes"
	} else {
		it.Status = "no"
	}
	if m := regexp.MustCompile(`,2,1,200,"([A-Z]{3})"`).FindStringSubmatch(body); m != nil {
		it.Region = m[1]
	}
	return it
}

func checkDisney(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "Disney+", Status: "unknown"}
	resp, _, err := get(ctx, "https://www.disneyplus.com/", false)
	if err != nil {
		return it
	}
	loc := resp.Header.Get("Location")
	switch {
	case resp.StatusCode == 403 || strings.Contains(loc, "unavailable") || strings.Contains(loc, "preview"):
		it.Status = "no"
	default:
		it.Status = "yes"
		if m := regexp.MustCompile(`disneyplus\.com/([a-z]{2})-`).FindStringSubmatch(loc); m != nil {
			it.Region = strings.ToUpper(m[1])
		}
	}
	return it
}

func checkTikTok(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "TikTok", Status: "unknown"}
	_, body, err := get(ctx, "https://www.tiktok.com/", true)
	if err != nil {
		return it
	}
	if m := regexp.MustCompile(`"region":"([A-Z]{2})"`).FindStringSubmatch(body); m != nil {
		it.Status, it.Region = "yes", m[1]
	} else {
		it.Status = "no"
	}
	return it
}

// ---------- IP quality ----------

type IPQualityResult struct {
	IP        string   `json:"ip"`
	ASN       string   `json:"asn"`
	Org       string   `json:"org"`
	Country   string   `json:"country"`
	City      string   `json:"city"`
	Type      string   `json:"type"`
	Risk      int      `json:"risk"`
	Proxy     bool     `json:"proxy"`
	Blacklist []string `json:"blacklist"`
	Checked   int      `json:"checked"`
}

var dnsbls = []string{
	"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "psbl.surriel.com",
	"dnsbl-1.uceprotect.net", "dnsbl.dronebl.org", "ix.dnsbl.manitu.net", "truncate.gbudb.net",
	"bl.mailspike.net", "all.s5h.net", "dnsbl.sorbs.net", "spam.dnsbl.anonmails.de",
}

func IPQuality(ctx context.Context, out func(string)) (*IPQualityResult, error) {
	r := &IPQualityResult{}
	_, body, err := get(ctx, "https://ipinfo.io/json", true)
	if err != nil {
		return nil, err
	}
	var info struct{ IP, City, Country, Org string }
	_ = json.Unmarshal([]byte(body), &info)
	r.IP, r.City, r.Country = info.IP, info.City, info.Country
	if asn, org, ok := strings.Cut(info.Org, " "); ok {
		r.ASN, r.Org = asn, org
	}
	out(fmt.Sprintf("IP %s  %s %s  %s %s\n", r.IP, r.Country, r.City, r.ASN, r.Org))

	if _, body, err := get(ctx, "https://proxycheck.io/v2/"+r.IP+"?vpn=1&risk=1&asn=1", true); err == nil {
		var pc map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &pc)
		var d struct {
			Proxy string `json:"proxy"`
			Type  string `json:"type"`
			Risk  int    `json:"risk"`
		}
		_ = json.Unmarshal(pc[r.IP], &d)
		r.Proxy, r.Type, r.Risk = d.Proxy == "yes", d.Type, d.Risk
		out(fmt.Sprintf("类型 %s  风险 %d  代理标记 %v\n", r.Type, r.Risk, r.Proxy))
	}

	ip := net.ParseIP(r.IP).To4()
	if ip != nil {
		rev := fmt.Sprintf("%d.%d.%d.%d", ip[3], ip[2], ip[1], ip[0])
		var mu sync.Mutex
		var wg sync.WaitGroup
		res := net.Resolver{}
		for _, bl := range dnsbls {
			wg.Add(1)
			go func(bl string) {
				defer wg.Done()
				c, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				addrs, err := res.LookupHost(c, rev+"."+bl)
				mu.Lock()
				r.Checked++
				// 127.255.255.x are "query refused" codes, not listings.
				if err == nil && len(addrs) > 0 && strings.HasPrefix(addrs[0], "127.0.0.") {
					r.Blacklist = append(r.Blacklist, bl)
				}
				mu.Unlock()
			}(bl)
		}
		wg.Wait()
		sort.Strings(r.Blacklist)
		out(fmt.Sprintf("黑名单 %d / %d 命中 %s\n", len(r.Blacklist), r.Checked, strings.Join(r.Blacklist, ", ")))
	}
	return r, nil
}

// ---------- Reality target scan ----------

type RealityCandidate struct {
	Domain    string  `json:"domain"`
	OK        bool    `json:"ok"`
	TLS13     bool    `json:"tls13"`
	H2        bool    `json:"h2"`
	X25519    bool    `json:"x25519"`
	Redirect  string  `json:"redirect,omitempty"`
	Handshake float64 `json:"handshake_ms"`
	Error     string  `json:"error,omitempty"`
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
		out(fmt.Sprintf("%-26s ok=%-5v tls13=%-5v h2=%-5v x25519=%-5v %6.1fms %s%s\n", r.Domain, r.OK, r.TLS13, r.H2, r.X25519, r.Handshake, r.Redirect, r.Error))
	}
	return res
}

func scanOne(ctx context.Context, domain string) RealityCandidate {
	r := RealityCandidate{Domain: domain}
	d := &net.Dialer{Timeout: 5 * time.Second}
	cfg := &tls.Config{ServerName: domain, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}, CurvePreferences: []tls.CurveID{tls.X25519}}
	start := time.Now()
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, "443"))
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
	r.X25519 = true // the handshake only offered X25519
	conn.Close()

	resp, _, err := get(ctx, "https://"+domain+"/", false)
	if err == nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if u, perr := http.NewRequest("GET", loc, nil); perr == nil && u.URL.Host != "" && u.URL.Host != domain {
			r.Redirect = "跳转到 " + u.URL.Host
		}
	}
	r.OK = r.TLS13 && r.H2 && r.X25519 && r.Redirect == ""
	return r
}
