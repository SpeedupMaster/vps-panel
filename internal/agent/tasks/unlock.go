package tasks

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

type UnlockItem struct {
	Name   string `json:"name"`
	Status string `json:"status"` // yes | partial | no | unknown
	Region string `json:"region,omitempty"`
	Detail string `json:"detail,omitempty"`
}

var (
	reCountry      = regexp.MustCompile(`"(?:countryCode|currentCountry|GL|region)":"([A-Z]{2})"`)
	reNFRegion     = regexp.MustCompile(`netflix\.com/([a-z]{2})(?:-[a-z]{2})?/`)
	reYTRegion     = regexp.MustCompile(`"INNERTUBE_CONTEXT_GL":"([A-Z]{2})"`)
	reDisneyLoc    = regexp.MustCompile(`disneyplus\.com/([a-z]{2})-`)
	reTikTokReg    = regexp.MustCompile(`"(?:region|countryCode)":"([A-Z]{2})"`)
	reGeminiFlag   = regexp.MustCompile(`45631641(?:,null,|\\?":)(true|false)`)
	reGeminiRegion = regexp.MustCompile(`,2,1,200,"([A-Z]{3})"`)
	tiktokBlocked  = map[string]bool{"CN": true, "HK": true, "RU": true, "IN": true}
	geminiBlocked  = map[string]bool{"CHN": true, "HKG": true, "MAC": true, "RUS": true, "BLR": true, "CUB": true, "IRN": true, "PRK": true, "SYR": true}
	alpha3to2      = map[string]string{
		"USA": "US", "CAN": "CA", "MEX": "MX", "BRA": "BR", "ARG": "AR", "GBR": "GB", "DEU": "DE", "FRA": "FR", "NLD": "NL",
		"ITA": "IT", "ESP": "ES", "CHE": "CH", "SWE": "SE", "POL": "PL", "FIN": "FI", "IRL": "IE", "TUR": "TR", "RUS": "RU",
		"JPN": "JP", "KOR": "KR", "SGP": "SG", "HKG": "HK", "TWN": "TW", "MAC": "MO", "CHN": "CN", "IND": "IN", "MYS": "MY",
		"THA": "TH", "VNM": "VN", "IDN": "ID", "PHL": "PH", "AUS": "AU", "NZL": "NZ", "ARE": "AE", "ISR": "IL", "ZAF": "ZA",
	}
)

func Unlock(ctx context.Context, out func(string)) []UnlockItem {
	defer sharedCheckTransport.CloseIdleConnections()
	checks := []func(context.Context) UnlockItem{
		checkNetflix,
		func(ctx context.Context) UnlockItem {
			return probeUnlock(ctx, "YouTube Premium", "https://www.youtube.com/premium", true, "", func(resp *http.Response, body string) (string, string, string) {
				finalURL := ""
				if resp.Request != nil && resp.Request.URL != nil {
					finalURL = resp.Request.URL.String()
				}
				return youtubeVerdict(resp.StatusCode, finalURL, body)
			})
		},
		checkChatGPT,
		func(ctx context.Context) UnlockItem {
			return probeUnlock(ctx, "Claude", "https://claude.ai/", false, "", func(resp *http.Response, body string) (string, string, string) {
				st, detail := claudeVerdict(resp.StatusCode, resp.Header.Get("Location"), resp.Header, body)
				return st, cfTraceLoc(ctx, "https://claude.ai/cdn-cgi/trace"), detail
			})
		},
		func(ctx context.Context) UnlockItem {
			return probeUnlock(ctx, "Gemini", "https://gemini.google.com/", true, "请求失败", func(_ *http.Response, body string) (string, string, string) {
				return geminiVerdict(body)
			})
		},
		func(ctx context.Context) UnlockItem {
			return probeUnlock(ctx, "Disney+", "https://www.disneyplus.com/", false, "", func(resp *http.Response, body string) (string, string, string) {
				return disneyVerdict(resp.StatusCode, resp.Header.Get("Location"), body)
			})
		},
		func(ctx context.Context) UnlockItem {
			return probeUnlock(ctx, "TikTok", "https://www.tiktok.com/", true, "", func(resp *http.Response, body string) (string, string, string) {
				return tiktokVerdict(resp.StatusCode, body)
			})
		},
	}
	res := make([]UnlockItem, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c func(context.Context) UnlockItem) { defer wg.Done(); res[i] = c(ctx) }(i, c)
	}
	wg.Wait()
	for _, r := range res {
		out(fmt.Sprintf("%-16s %-8s %s %s\n", r.Name, r.Status, r.Region, r.Detail))
	}
	return res
}

func probeUnlock(ctx context.Context, name, rawURL string, follow bool, errDetail string, eval func(*http.Response, string) (string, string, string)) UnlockItem {
	it := UnlockItem{Name: name, Status: "unknown"}
	resp, body, err := get(ctx, rawURL, follow)
	if err != nil {
		it.Detail = errDetail
		return it
	}
	it.Status, it.Region, it.Detail = eval(resp, body)
	return it
}

func netflixVerdict(r1Code int, r1Loc, r1Body string, r2Code int) (status, region, detail string) {
	okCode := func(code int) bool { return code == 200 || (code >= 300 && code < 400) }
	switch {
	case r1Code == 403:
		return "no", "", "IP 被封锁"
	case okCode(r1Code) && okCode(r2Code) && r2Code != 404:
		status, detail = "yes", "完整解锁"
	case okCode(r1Code):
		status, detail = "partial", "仅自制剧"
	default:
		return "no", "", ""
	}
	if m := reNFRegion.FindStringSubmatch(r1Loc); m != nil {
		region = strings.ToUpper(m[1])
	} else if m := reCountry.FindStringSubmatch(r1Body); m != nil {
		region = m[1]
	}
	return status, region, detail
}

func checkNetflix(ctx context.Context) UnlockItem {
	it := UnlockItem{Name: "Netflix", Status: "unknown"}
	r1, body1, err1 := get(ctx, "https://www.netflix.com/title/81280792", false)
	r2, _, err2 := get(ctx, "https://www.netflix.com/title/70143836", false)
	if err1 != nil || err2 != nil {
		it.Detail = "请求失败"
		return it
	}
	it.Status, it.Region, it.Detail = netflixVerdict(r1.StatusCode, r1.Header.Get("Location"), body1, r2.StatusCode)
	return it
}

func youtubeVerdict(status int, finalURL, body string) (statusStr, region, detail string) {
	lowerURL := strings.ToLower(finalURL)
	if strings.Contains(lowerURL, "consent.youtube.com") || strings.Contains(lowerURL, "consent.google.com") ||
		strings.Contains(body, "Before you continue to YouTube") || strings.Contains(body, "consent.youtube.com") {
		return "unknown", "", "被重定向到隐私同意页"
	}
	if status >= 400 {
		return "unknown", "", fmt.Sprintf("HTTP %d", status)
	}
	if strings.Contains(body, "Premium is not available in your country") || strings.Contains(body, "YouTube Premium is not available") {
		return "no", "", "地区不支持"
	}
	if m := reYTRegion.FindStringSubmatch(body); m != nil {
		region = m[1]
	} else if m := reCountry.FindStringSubmatch(body); m != nil {
		region = m[1]
	}
	if region != "" || strings.Contains(body, "INNERTUBE_CONTEXT") || strings.Contains(body, "YouTube Premium") {
		return "yes", region, ""
	}
	return "unknown", "", "无法确认页面内容"
}

func cfTraceLoc(ctx context.Context, rawURL string) string {
	_, body, err := get(ctx, rawURL, true)
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
	it := UnlockItem{Name: "ChatGPT", Status: "unknown", Region: cfTraceLoc(ctx, "https://chatgpt.com/cdn-cgi/trace")}
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

func claudeVerdict(status int, loc string, hdr http.Header, body string) (statusStr, detail string) {
	switch {
	case strings.Contains(strings.ToLower(loc), "unavailable"):
		return "no", "地区不支持"
	case status == http.StatusForbidden || status == http.StatusServiceUnavailable ||
		strings.EqualFold(hdr.Get("cf-mitigated"), "challenge") ||
		strings.Contains(body, "Just a moment") || strings.Contains(body, "cf-browser-verification"):
		return "unknown", fmt.Sprintf("被 Cloudflare 拦截 (HTTP %d)", status)
	case status >= 400:
		return "unknown", fmt.Sprintf("HTTP %d", status)
	case status == http.StatusOK || (status >= 300 && status < 400):
		return "yes", ""
	default:
		return "unknown", ""
	}
}

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

func disneyVerdict(status int, loc, body string) (statusStr, region, detail string) {
	lowerLoc := strings.ToLower(loc)
	switch {
	case status == http.StatusForbidden || strings.Contains(lowerLoc, "unavailable") || strings.Contains(lowerLoc, "preview") ||
		strings.Contains(body, "Disney+ is not available") || strings.Contains(body, "not available in your region"):
		return "no", "", "地区不支持"
	case status >= 400:
		return "unknown", "", fmt.Sprintf("HTTP %d", status)
	case status >= 300 && status < 400:
		if m := reDisneyLoc.FindStringSubmatch(lowerLoc); m != nil {
			return "yes", strings.ToUpper(m[1]), ""
		}
		if loc != "" {
			return "yes", "", ""
		}
		return "unknown", "", "未检测到地区跳转"
	case status == http.StatusOK:
		if m := reDisneyLoc.FindStringSubmatch(strings.ToLower(body)); m != nil {
			return "yes", strings.ToUpper(m[1]), ""
		}
		if m := reCountry.FindStringSubmatch(body); m != nil {
			return "yes", m[1], ""
		}
		return "unknown", "", "页面为 SPA 占位，无法确认"
	default:
		return "unknown", "", ""
	}
}

func tiktokVerdict(status int, body string) (statusStr, region, detail string) {
	if status == http.StatusForbidden || status == http.StatusUnavailableForLegalReasons {
		return "no", "", fmt.Sprintf("HTTP %d", status)
	}
	if status >= 400 {
		return "unknown", "", fmt.Sprintf("HTTP %d", status)
	}
	if m := reTikTokReg.FindStringSubmatch(body); m != nil {
		if tiktokBlocked[m[1]] {
			return "no", m[1], "地区不支持"
		}
		return "yes", m[1], ""
	}
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "captcha") || strings.Contains(lower, "verifycenter") || strings.Contains(lower, "access denied"):
		return "unknown", "", "触发人机验证或页面受限"
	case strings.Contains(lower, "not available in your country") || strings.Contains(lower, "service is not available"):
		return "no", "", "地区不支持"
	default:
		return "unknown", "", "未检测到地区信息"
	}
}
