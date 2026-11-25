package tasks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
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
		it.Detail = "请求失败"
		return it
	}
	it.Status, it.Region, it.Detail = geminiVerdict(body)
	return it
}

var (
	// Availability flag in the page's bootstrap data. Google moved it from an
	// array ("45631641,null,true") to an escaped JSON object ("45631641\":true").
	reGeminiFlag   = regexp.MustCompile(`45631641(?:,null,|\\?":)(true|false)`)
	reGeminiRegion = regexp.MustCompile(`,2,1,200,"([A-Z]{3})"`)
	// Regions where the Gemini app is not offered.
	geminiBlocked = map[string]bool{"CHN": true, "HKG": true, "MAC": true, "RUS": true, "BLR": true, "CUB": true, "IRN": true, "PRK": true, "SYR": true}
	alpha3to2     = map[string]string{
		"USA": "US", "CAN": "CA", "MEX": "MX", "BRA": "BR", "ARG": "AR", "GBR": "GB", "DEU": "DE", "FRA": "FR", "NLD": "NL",
		"ITA": "IT", "ESP": "ES", "CHE": "CH", "SWE": "SE", "POL": "PL", "FIN": "FI", "IRL": "IE", "TUR": "TR", "RUS": "RU",
		"JPN": "JP", "KOR": "KR", "SGP": "SG", "HKG": "HK", "TWN": "TW", "MAC": "MO", "CHN": "CN", "IND": "IN", "MYS": "MY",
		"THA": "TH", "VNM": "VN", "IDN": "ID", "PHL": "PH", "AUS": "AU", "NZL": "NZ", "ARE": "AE", "ISR": "IL", "ZAF": "ZA",
	}
)

// geminiVerdict reads gemini.google.com's HTML. The flag decides when present;
// otherwise the detected region does, so a page change can't mark every node as blocked.
func geminiVerdict(body string) (status, region, detail string) {
	if m := reGeminiRegion.FindStringSubmatch(body); m != nil {
		region = m[1]
	}
	blocked := geminiBlocked[region]
	if r, ok := alpha3to2[region]; ok {
		region = r
	}
	switch m := reGeminiFlag.FindStringSubmatch(body); {
	case m != nil && m[1] == "true":
		return "yes", region, ""
	case m != nil:
		return "no", region, "地区不支持"
	case region == "":
		return "unknown", "", "页面结构已变化，无法判断"
	case blocked:
		return "no", region, "地区不支持"
	default:
		return "yes", region, ""
	}
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
	// Per-provider verdicts. Type/Risk/Proxy above mirror the proxycheck.io
	// entry so older summaries and UIs keep working.
	Sources []IPSource `json:"sources"`
}

// IPSource is one provider's verdict on the IP. Error is set when the lookup
// failed, so the UI can tell "no flags" apart from "didn't get an answer";
// Skipped says why a source wasn't queried at all.
type IPSource struct {
	Name    string   `json:"name"`
	Type    string   `json:"type,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	Score   string   `json:"score,omitempty"`
	Error   string   `json:"error,omitempty"`
	Skipped string   `json:"skipped,omitempty"`
}

func (s IPSource) line() string {
	if s.Skipped != "" {
		return fmt.Sprintf("%-14s 跳过：%s\n", s.Name, s.Skipped)
	}
	if s.Error != "" {
		return fmt.Sprintf("%-14s 查询失败：%s\n", s.Name, s.Error)
	}
	parts := []string{}
	if s.Type != "" {
		parts = append(parts, "类型 "+s.Type)
	}
	if len(s.Flags) > 0 {
		parts = append(parts, strings.Join(s.Flags, " "))
	} else {
		parts = append(parts, "无标记")
	}
	if s.Score != "" {
		parts = append(parts, s.Score)
	}
	return fmt.Sprintf("%-14s %s\n", s.Name, strings.Join(parts, "  "))
}

func flagIf(flags []string, cond bool, name string) []string {
	if cond {
		return append(flags, name)
	}
	return flags
}

// getJSON fetches url and decodes the body into v, treating non-200 as an error.
func getJSON(ctx context.Context, url string, v any) error {
	resp, body, err := get(ctx, url, true)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		if e.Error == "" {
			e.Error = e.Message
		}
		if resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(e.Error), "api key") {
			return fmt.Errorf("API key 无效")
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("超出频率限制 (HTTP 429)")
		}
		if e.Error != "" {
			return fmt.Errorf("HTTP %d：%s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		return fmt.Errorf("响应解析失败")
	}
	return nil
}

func errText(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "超时"
	}
	return err.Error()
}

var dnsbls = []string{
	"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "psbl.surriel.com",
	"dnsbl-1.uceprotect.net", "dnsbl.dronebl.org", "ix.dnsbl.manitu.net", "truncate.gbudb.net",
	"bl.mailspike.net", "all.s5h.net", "dnsbl.sorbs.net", "spam.dnsbl.anonmails.de",
}

func IPQuality(ctx context.Context, keys proto.IPQualityKeys, out func(string)) (*IPQualityResult, error) {
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

	// Providers and DNSBLs are independent; query them all at once.
	providers := []func(context.Context, string) IPSource{
		func(ctx context.Context, ip string) IPSource {
			s, typ, risk, proxy := checkProxyCheck(ctx, ip)
			r.Type, r.Risk, r.Proxy = typ, risk, proxy
			return s
		},
		func(ctx context.Context, ip string) IPSource { return checkIPAPIIs(ctx, ip, keys.IPAPIIs) },
		checkIPAPICom,
	}
	r.Sources = make([]IPSource, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Sources[i] = p(ctx, r.IP)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.Blacklist, r.Checked = checkDNSBL(ctx, r.IP)
	}()
	wg.Wait()

	for _, s := range r.Sources {
		out(s.line())
	}
	if r.Checked > 0 {
		out(fmt.Sprintf("黑名单 %d / %d 命中 %s\n", len(r.Blacklist), r.Checked, strings.Join(r.Blacklist, ", ")))
	} else {
		out("黑名单 未检查（仅支持 IPv4）\n")
	}
	return r, nil
}

func checkProxyCheck(ctx context.Context, ip string) (s IPSource, typ string, risk int, proxy bool) {
	s.Name = "proxycheck.io"
	var pc map[string]json.RawMessage
	if err := getJSON(ctx, "https://proxycheck.io/v2/"+ip+"?vpn=1&risk=1&asn=1", &pc); err != nil {
		s.Error = errText(err)
		return
	}
	var status, msg string
	_ = json.Unmarshal(pc["status"], &status)
	_ = json.Unmarshal(pc["message"], &msg)
	if status == "denied" || status == "error" || pc[ip] == nil {
		s.Error = msg
		if s.Error == "" {
			s.Error = "无结果 (" + status + ")"
		}
		return
	}
	var d struct {
		Proxy string `json:"proxy"`
		Type  string `json:"type"`
		Risk  int    `json:"risk"`
	}
	_ = json.Unmarshal(pc[ip], &d)
	typ, risk, proxy = d.Type, d.Risk, d.Proxy == "yes"
	s.Type = d.Type
	s.Flags = flagIf(s.Flags, proxy, "代理")
	s.Score = fmt.Sprintf("风险 %d", d.Risk)
	return
}

// ipapi.is: since 2026-09 the detection flags need a (free) account key; the
// anonymous tier only returns owner/geo data, so without a key we skip it.
func checkIPAPIIs(ctx context.Context, ip, key string) IPSource {
	s := IPSource{Name: "ipapi.is"}
	if key == "" {
		s.Skipped = "未配置 API key"
		return s
	}
	var d struct {
		Error        string `json:"error"`
		IsDatacenter bool   `json:"is_datacenter"`
		IsVPN        bool   `json:"is_vpn"`
		IsProxy      bool   `json:"is_proxy"`
		IsTor        bool   `json:"is_tor"`
		IsAbuser     bool   `json:"is_abuser"`
		IsMobile     bool   `json:"is_mobile"`
		IsCrawler    bool   `json:"is_crawler"`
		Company      struct {
			Type        string `json:"type"`
			AbuserScore string `json:"abuser_score"`
		} `json:"company"`
	}
	q := url.Values{"q": {ip}, "key": {key}}
	if err := getJSON(ctx, "https://api.ipapi.is/?"+q.Encode(), &d); err != nil {
		s.Error = errText(err)
		return s
	}
	if d.Error != "" {
		s.Error = d.Error
		return s
	}
	s.Type = d.Company.Type
	s.Flags = flagIf(s.Flags, d.IsDatacenter, "机房")
	s.Flags = flagIf(s.Flags, d.IsVPN, "VPN")
	s.Flags = flagIf(s.Flags, d.IsProxy, "代理")
	s.Flags = flagIf(s.Flags, d.IsTor, "Tor")
	s.Flags = flagIf(s.Flags, d.IsAbuser, "滥用")
	s.Flags = flagIf(s.Flags, d.IsMobile, "移动网络")
	s.Flags = flagIf(s.Flags, d.IsCrawler, "爬虫")
	if d.Company.AbuserScore != "" {
		s.Score = "滥用分 " + d.Company.AbuserScore
	}
	return s
}

// ip-api.com: free, no key, 45 req/min, HTTP only on the free tier.
func checkIPAPICom(ctx context.Context, ip string) IPSource {
	s := IPSource{Name: "ip-api.com"}
	var d struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Proxy   bool   `json:"proxy"`
		Hosting bool   `json:"hosting"`
		Mobile  bool   `json:"mobile"`
	}
	if err := getJSON(ctx, "http://ip-api.com/json/"+ip+"?fields=status,message,proxy,hosting,mobile", &d); err != nil {
		s.Error = errText(err)
		return s
	}
	if d.Status != "success" {
		s.Error = d.Message
		if s.Error == "" {
			s.Error = "无结果"
		}
		return s
	}
	s.Flags = flagIf(s.Flags, d.Hosting, "机房")
	s.Flags = flagIf(s.Flags, d.Proxy, "代理")
	s.Flags = flagIf(s.Flags, d.Mobile, "移动网络")
	return s
}

// checkDNSBL looks the IPv4 address up in each DNSBL; IPv6 is skipped (checked = 0).
func checkDNSBL(ctx context.Context, addr string) (listed []string, checked int) {
	ip := net.ParseIP(addr).To4()
	if ip == nil {
		return nil, 0
	}
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
			checked++
			// 127.255.255.x are "query refused" codes, not listings.
			if err == nil && len(addrs) > 0 && strings.HasPrefix(addrs[0], "127.0.0.") {
				listed = append(listed, bl)
			}
			mu.Unlock()
		}(bl)
	}
	wg.Wait()
	sort.Strings(listed)
	return listed, checked
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
		out(fmt.Sprintf("%-26s ok=%-5v tls13=%-5v h2=%-5v x25519=%-5v %6.1fms %s%s%s\n", r.Domain, r.OK, r.TLS13, r.H2, r.X25519, r.Handshake, r.Redirect, r.Error, cdn))
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
	cfg := &tls.Config{ServerName: domain, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}, CurvePreferences: []tls.CurveID{tls.X25519}}
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
	r.X25519 = true // the handshake only offered X25519
	conn.Close()

	// Pin the validated public IP for the HTTP check too so DNS rebinding cannot
	// redirect it to an internal address.
	pinnedClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			ForceAttemptHTTP2: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, perr := net.SplitHostPort(addr)
				if perr != nil {
					port = "443"
				}
				return d.DialContext(ctx, network, net.JoinHostPort(targetIP, port))
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var resp *http.Response
	if req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil); rerr == nil {
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err = pinnedClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
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
