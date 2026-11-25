package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
)

type IPQualityResult struct {
	IP        string     `json:"ip"`
	ASN       string     `json:"asn"`
	Org       string     `json:"org"`
	Country   string     `json:"country"`
	City      string     `json:"city"`
	Type      string     `json:"type"`
	Risk      int        `json:"risk"`
	Proxy     bool       `json:"proxy"`
	Blacklist []string   `json:"blacklist"`
	Checked   int        `json:"checked"`
	Sources   []IPSource `json:"sources"` // per-provider verdicts; top fields mirror proxycheck.io
}

// IPSource is one provider's verdict on the IP.
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
	var parts []string
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

var dnsbls = []string{
	"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "psbl.surriel.com",
	"dnsbl-1.uceprotect.net", "dnsbl.dronebl.org", "ix.dnsbl.manitu.net", "truncate.gbudb.net",
	"bl.mailspike.net", "all.s5h.net", "dnsbl.sorbs.net", "spam.dnsbl.anonmails.de",
}

func IPQuality(ctx context.Context, keys proto.IPQualityKeys, out func(string)) (*IPQualityResult, error) {
	defer sharedCheckTransport.CloseIdleConnections()
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
		go func(i int, p func(context.Context, string) IPSource) { defer wg.Done(); r.Sources[i] = p(ctx, r.IP) }(i, p)
	}
	wg.Add(1)
	go func() { defer wg.Done(); r.Blacklist, r.Checked = checkDNSBL(ctx, r.IP) }()
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
		Proxy, Type string
		Risk        int
	}
	_ = json.Unmarshal(pc[ip], &d)
	typ, risk, proxy = d.Type, d.Risk, d.Proxy == "yes"
	s.Type, s.Flags, s.Score = d.Type, flagIf(s.Flags, proxy, "代理"), fmt.Sprintf("风险 %d", d.Risk)
	return
}

// ipapi.is: detection flags require a free account key; skip when unconfigured.
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
	for _, f := range []struct {
		on   bool
		name string
	}{{d.IsDatacenter, "机房"}, {d.IsVPN, "VPN"}, {d.IsProxy, "代理"}, {d.IsTor, "Tor"}, {d.IsAbuser, "滥用"}, {d.IsMobile, "移动网络"}, {d.IsCrawler, "爬虫"}} {
		s.Flags = flagIf(s.Flags, f.on, f.name)
	}
	if d.Company.AbuserScore != "" {
		s.Score = "滥用分 " + d.Company.AbuserScore
	}
	return s
}

// ip-api.com: free, no key, 45 req/min, HTTP only on the free tier.
func checkIPAPICom(ctx context.Context, ip string) IPSource {
	s := IPSource{Name: "ip-api.com"}
	var d struct {
		Status, Message        string
		Proxy, Hosting, Mobile bool
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
	s.Flags = flagIf(flagIf(flagIf(s.Flags, d.Hosting, "机房"), d.Proxy, "代理"), d.Mobile, "移动网络")
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
