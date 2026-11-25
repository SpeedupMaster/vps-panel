package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

// ---------- serving & per-instance rate limiter ----------

type SubResponse struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

type subRateLimiter struct {
	mu     sync.Mutex
	hits   map[uint][]time.Time
	ipHits map[string][]time.Time
}

func newSubRateLimiter() *subRateLimiter {
	return &subRateLimiter{
		hits:   map[uint][]time.Time{},
		ipHits: map[string][]time.Time{},
	}
}

func (p *Panel) subLimiter() *subRateLimiter {
	if p.subLim == nil {
		p.subLim = newSubRateLimiter()
	}
	return p.subLim
}

const (
	subPerTokenPerMin = 20
	subPerIPPerMin    = 60
	subMaxTrackedIPs  = 4096
)

func subIPRateKey(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return strings.TrimSpace(ip)
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func (l *subRateLimiter) allowIP(ip string) bool {
	key := subIPRateKey(ip)
	if key == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.ipHits) >= 256 {
		for k, ts := range l.ipHits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Minute {
				delete(l.ipHits, k)
			}
		}
	}
	if _, exists := l.ipHits[key]; !exists && len(l.ipHits) >= subMaxTrackedIPs {
		var oldestKey string
		var oldest time.Time
		for k, ts := range l.ipHits {
			t := time.Time{}
			if len(ts) > 0 {
				t = ts[len(ts)-1]
			}
			if oldestKey == "" || t.Before(oldest) {
				oldestKey, oldest = k, t
			}
		}
		if oldestKey != "" {
			delete(l.ipHits, oldestKey)
		}
	}
	var keep []time.Time
	for _, t := range l.ipHits[key] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= subPerIPPerMin {
		l.ipHits[key] = keep
		return false
	}
	l.ipHits[key] = append(keep, now)
	return true
}

func (l *subRateLimiter) allowToken(id uint) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range l.hits[id] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= subPerTokenPerMin {
		l.hits[id] = keep
		return false
	}
	l.hits[id] = append(keep, now)
	return true
}

func (l *subRateLimiter) clearToken(id uint) {
	l.mu.Lock()
	delete(l.hits, id)
	l.mu.Unlock()
}

var ErrSubNotFound = errors.New("not found")
var ErrSubRateLimited = errors.New("too many requests")

// ServeSub resolves a token and renders the subscription.
func (p *Panel) ServeSub(token, target, ua, ip string) (*SubResponse, error) {
	lim := p.subLimiter()
	if !lim.allowIP(ip) {
		return nil, ErrSubRateLimited
	}
	var t store.SubToken
	if err := p.St.R().First(&t, "token_hash = ?", secret.Hash(token)).Error; err != nil || !t.Enabled {
		return nil, ErrSubNotFound
	}
	if !lim.allowToken(t.ID) {
		return nil, ErrSubRateLimited
	}
	if target == "" {
		target = t.TargetDefault
	}
	if target == "" || target == "auto" {
		target = sub.Detect(ua)
	}
	if target == "clash" || target == "meta" {
		target = "mihomo"
	}
	if target == "sing-box" {
		target = "singbox"
	}
	p.logSubAccess(&t, ip, ua, target)

	nodes := p.SubNodesFor(&t)
	profileName := strings.TrimSpace(p.St.Get("sub_profile_name", ""))
	if profileName == "" {
		profileName = "vps-panel"
	}
	res := &SubResponse{Headers: map[string]string{
		"Profile-Title":           formatProfileTitle(profileName),
		"Profile-Update-Interval": strconv.Itoa(p.St.GetInt("sub_update_hours", 12)),
		"Content-Disposition":     formatContentDisposition(profileName),
	}}
	if ui := p.subUserinfoFor(&t); ui != "" {
		res.Headers["Subscription-Userinfo"] = ui
	}
	var err error
	switch target {
	case "mihomo":
		res.Body, err = sub.Mihomo(nodes, p.activeTemplate("mihomo"))
		res.ContentType = "text/yaml; charset=utf-8"
	case "singbox":
		res.Body, err = sub.SingBox(nodes, p.activeTemplate("singbox"))
		res.ContentType = "application/json; charset=utf-8"
	default:
		res.Body = []byte(sub.Base64(nodes))
		res.ContentType = "text/plain; charset=utf-8"
	}
	return res, err
}

// PreviewSub renders the global default subscription without logging or rate limiting.
func (p *Panel) PreviewSub(target string) (string, error) {
	s, _, err := p.PreviewSubFor(target, 0)
	return s, err
}

// PreviewSubFor renders the subscription for tokenID (0 = global default)
// without logging or rate limiting, returning the rendered body and node count.
func (p *Panel) PreviewSubFor(target string, tokenID uint) (string, int, error) {
	var tok *store.SubToken
	if tokenID > 0 {
		var t store.SubToken
		if err := p.St.DB.First(&t, tokenID).Error; err != nil {
			return "", 0, errors.New("设备订阅不存在")
		}
		tok = &t
	}
	nodes := p.SubNodesFor(tok)
	switch target {
	case "mihomo":
		b, err := sub.Mihomo(nodes, p.activeTemplate("mihomo"))
		return string(b), len(nodes), err
	case "singbox":
		b, err := sub.SingBox(nodes, p.activeTemplate("singbox"))
		return string(b), len(nodes), err
	}
	var links []string
	for _, n := range nodes {
		links = append(links, sub.Link(n))
	}
	return strings.Join(links, "\n"), len(nodes), nil
}

func (p *Panel) activeTemplate(target string) string {
	var t store.SubTemplate
	if err := p.St.R().Where("target = ? AND active = ?", target, true).First(&t).Error; err == nil {
		return t.Content
	}
	return ""
}

// ValidateTemplate renders the template with the current nodes.
func ValidateTemplate(target, content string, nodes []sub.Node) error {
	var err error
	switch target {
	case "mihomo":
		_, err = sub.Mihomo(nodes, content)
	case "singbox":
		_, err = sub.SingBox(nodes, content)
	default:
		err = errors.New("模板类型只能是 mihomo / singbox")
	}
	return err
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func formatProfileTitle(name string) string {
	ascii := true
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] > 0x7e {
			ascii = false
			break
		}
	}
	if ascii && !strings.HasPrefix(strings.ToLower(name), "base64:") {
		return name
	}
	return "base64:" + base64.StdEncoding.EncodeToString([]byte(name))
}

func formatContentDisposition(name string) string {
	esc := urlPathEscape(name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, esc, esc)
}

func sameNet(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia == nil || ib == nil {
		return a == b
	}
	if ia.To4() != nil && ib.To4() != nil {
		return ia.Mask(net.CIDRMask(24, 32)).Equal(ib.Mask(net.CIDRMask(24, 32)))
	}
	return ia.Mask(net.CIDRMask(48, 128)).Equal(ib.Mask(net.CIDRMask(48, 128)))
}

func (p *Panel) logSubAccess(t *store.SubToken, ip, ua, target string) {
	var recent []store.SubAccessLog
	p.St.DB.Select("ip").Where("token_id = ? AND ts > ?", t.ID, time.Now().AddDate(0, 0, -30)).Group("ip").Find(&recent)
	stranger := len(recent) > 0
	for _, r := range recent {
		if sameNet(r.IP, ip) {
			stranger = false
			break
		}
	}
	now := time.Now()
	p.St.DB.Create(&store.SubAccessLog{TokenID: t.ID, IP: ip, UA: truncate(ua, 200), Target: target, Stranger: stranger, TS: now})
	p.St.DB.Model(t).Updates(map[string]any{"last_used_at": &now, "last_ip": ip})
	if stranger {
		p.Alert.Info("stranger_sub", 0, fmt.Sprintf("订阅「%s」被新 IP %s 拉取（UA: %s）。如非本人操作，请重置该订阅。", t.Name, ip, truncate(ua, 60)))
	}
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

func (p *Panel) SubAccessLogs(tokenID uint, limit int) []store.SubAccessLog {
	var logs []store.SubAccessLog
	q := p.St.R().Order("id DESC").Limit(limit)
	if tokenID > 0 {
		q = q.Where("token_id = ?", tokenID)
	}
	q.Find(&logs)
	return logs
}
