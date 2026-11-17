package service

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

// ---------- tokens ----------

type SubTokenView struct {
	store.SubToken
	URL      string `json:"url"`
	Accesses int64  `json:"accesses_7d"`
}

func (p *Panel) SubBase() string {
	base := p.St.Get("sub_url", "")
	if base == "" {
		base = p.St.Get("panel_url", "")
	}
	return strings.TrimRight(base, "/")
}

func (p *Panel) SubTokens() []SubTokenView {
	var ts []store.SubToken
	p.St.DB.Order("id").Find(&ts)
	out := make([]SubTokenView, 0, len(ts))
	for _, t := range ts {
		v := SubTokenView{SubToken: t}
		if tok, err := p.Box.Open(t.TokenEnc); err == nil {
			v.URL = p.SubBase() + "/s/" + tok
		}
		p.St.DB.Model(&store.SubAccessLog{}).Where("token_id = ? AND ts > ?", t.ID, time.Now().AddDate(0, 0, -7)).Count(&v.Accesses)
		out = append(out, v)
	}
	return out
}

func (p *Panel) CreateSubToken(name, target string) (*store.SubToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("请填写设备名称")
	}
	if target != "" && target != "mihomo" && target != "singbox" && target != "base64" {
		return nil, errors.New("格式只能是 mihomo / singbox / base64")
	}
	tok := secret.RandomToken(24)
	enc := p.Box.Seal(tok)
	t := &store.SubToken{Name: name, TokenEnc: enc, TokenHash: secret.Hash(tok), TargetDefault: target, Enabled: true, CreatedAt: time.Now()}
	return t, p.St.DB.Create(t).Error
}

// ResetSubToken issues a new secret for a device (the old URL stops working).
func (p *Panel) ResetSubToken(id uint) error {
	tok := secret.RandomToken(24)
	enc := p.Box.Seal(tok)
	return p.St.DB.Model(&store.SubToken{}).Where("id = ?", id).Updates(map[string]any{"token_enc": enc, "token_hash": secret.Hash(tok)}).Error
}

// ---------- nodes ----------

func nodeFor(r *store.Route, entry, landing *store.Server, ri *store.RealityInbound, tpl string) sub.Node {
	if tpl == "" {
		tpl = "{flag} {name}"
	}
	flag := sub.Flag(entry.CountryCode)
	region := entry.CountryCode
	landingName := ""
	if r.Type == "chain" && landing != nil {
		flag += "→" + sub.Flag(landing.CountryCode)
		region = landing.CountryCode
		landingName = landing.Name
	}
	name := strings.NewReplacer("{flag}", flag, "{name}", r.Name, "{entry}", entry.Name, "{landing}", landingName).Replace(tpl)
	addr := ri.Address
	if addr == "" {
		addr = serverAddr(entry)
	}
	sni, _, _ := strings.Cut(ri.ServerNames, ",")
	sid, _, _ := strings.Cut(ri.ShortIDs, ",")
	fp := ri.Fingerprint
	if fp == "" {
		fp = "chrome"
	}
	return sub.Node{
		Name: strings.TrimSpace(name), Server: addr, Port: ri.Port, UUID: r.UUID, SNI: sni,
		PublicKey: ri.PublicKey, ShortID: sid, FP: fp, RegionCode: region,
	}
}

func VLESSLink(n sub.Node) string { return sub.Link(n) }

// SubNodes lists the routes that go into subscriptions.
func (p *Panel) SubNodes() []sub.Node {
	var rs []store.Route
	p.St.DB.Where("enabled = ? AND in_sub = ?", true, true).Order("sort, id").Find(&rs)
	var svs []store.Server
	p.St.DB.Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	p.St.DB.Find(&ris)
	riBy := map[uint]*store.RealityInbound{}
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	exBlocked := p.St.GetBool("sub_exclude_blocked", true)
	exOffline := p.St.GetBool("sub_exclude_offline", false)
	tpl := p.St.Get("sub_name_tpl", "{flag} {name}")
	seen := map[string]int{}
	var nodes []sub.Node
	for i := range rs {
		r := &rs[i]
		e, ri := byID[r.EntryServerID], riBy[r.EntryServerID]
		if e == nil || ri == nil || serverAddr(e) == "" && ri.Address == "" {
			continue
		}
		if exBlocked && e.Blocked || exOffline && e.Status == "offline" {
			continue
		}
		n := nodeFor(r, e, byID[r.LandingServerID], ri, tpl)
		if c := seen[n.Name]; c > 0 { // names must be unique for clients
			n.Name = fmt.Sprintf("%s %d", n.Name, c+1)
		}
		seen[n.Name]++
		nodes = append(nodes, n)
	}
	return nodes
}

// ---------- serving ----------

type SubResponse struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

var subLimiter = struct {
	sync.Mutex
	hits map[uint][]time.Time
}{hits: map[uint][]time.Time{}}

func allowSub(id uint) bool {
	subLimiter.Lock()
	defer subLimiter.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range subLimiter.hits[id] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 20 {
		subLimiter.hits[id] = keep
		return false
	}
	subLimiter.hits[id] = append(keep, now)
	return true
}

var ErrSubNotFound = errors.New("not found")
var ErrSubRateLimited = errors.New("too many requests")

// ServeSub resolves a token and renders the subscription.
func (p *Panel) ServeSub(token, target, ua, ip string) (*SubResponse, error) {
	var t store.SubToken
	if err := p.St.DB.First(&t, "token_hash = ?", secret.Hash(token)).Error; err != nil || !t.Enabled {
		return nil, ErrSubNotFound
	}
	if !allowSub(t.ID) {
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

	nodes := p.SubNodes()
	res := &SubResponse{Headers: map[string]string{
		"Profile-Update-Interval": strconv.Itoa(p.St.GetInt("sub_update_hours", 12)),
		"Content-Disposition":     "attachment; filename*=UTF-8''" + urlPathEscape(p.St.Get("sub_profile_name", "vps-panel")),
	}}
	if ui := p.subUserinfo(); ui != "" {
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

// Preview renders without logging or rate limiting (admin UI).
func (p *Panel) PreviewSub(target string) (string, error) {
	nodes := p.SubNodes()
	switch target {
	case "mihomo":
		b, err := sub.Mihomo(nodes, p.activeTemplate("mihomo"))
		return string(b), err
	case "singbox":
		b, err := sub.SingBox(nodes, p.activeTemplate("singbox"))
		return string(b), err
	}
	var links []string
	for _, n := range nodes {
		links = append(links, sub.Link(n))
	}
	return strings.Join(links, "\n"), nil
}

func (p *Panel) activeTemplate(target string) string {
	var t store.SubTemplate
	if err := p.St.DB.Where("target = ? AND active = ?", target, true).First(&t).Error; err == nil {
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

// subUserinfo sums the billing-cycle usage of all servers (or the one set in
// sub_userinfo).
func (p *Panel) subUserinfo() string {
	mode := p.St.Get("sub_userinfo", "all")
	if mode == "none" {
		return ""
	}
	var svs []store.Server
	q := p.St.DB
	if id, err := strconv.Atoi(mode); err == nil {
		q = q.Where("id = ?", id)
	}
	q.Find(&svs)
	var up, down, total uint64
	var expire int64
	for i := range svs {
		u := p.CycleUsage(&svs[i])
		up, down = up+u.Up, down+u.Down
		if svs[i].QuotaBytes > 0 {
			total += uint64(svs[i].QuotaBytes)
		}
		if svs[i].ExpireAt != nil && (expire == 0 || svs[i].ExpireAt.Unix() < expire) && svs[i].Mode == "node" {
			expire = svs[i].ExpireAt.Unix()
		}
	}
	return sub.Userinfo(up, down, total, expire)
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

// logSubAccess records the pull and alerts on an IP never seen for this
// token in the last 30 days (after the token's first use).
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
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (p *Panel) SubAccessLogs(tokenID uint, limit int) []store.SubAccessLog {
	var logs []store.SubAccessLog
	q := p.St.DB.Order("id DESC").Limit(limit)
	if tokenID > 0 {
		q = q.Where("token_id = ?", tokenID)
	}
	q.Find(&logs)
	return logs
}
