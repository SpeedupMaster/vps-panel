package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

// ---------- tokens ----------

type SubTokenView struct {
	store.SubToken
	Token    string `json:"token"`
	URL      string `json:"url"`
	Accesses int64  `json:"accesses_7d"`
}

func normalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

func urlHost(raw string) string {
	s := normalizeBaseURL(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (p *Panel) SubBase() string {
	base := normalizeBaseURL(p.St.Get("sub_url", ""))
	if base == "" {
		base = normalizeBaseURL(p.St.Get("panel_url", ""))
	}
	return base
}

// SubOnlyHost returns the lowercase hostname of sub_url when both panel_url and
// sub_url are configured with distinct hosts. Requests on this host are
// restricted to subscription endpoints only.
func (p *Panel) SubOnlyHost() string {
	subHost := urlHost(p.St.Get("sub_url", ""))
	panelHost := urlHost(p.St.Get("panel_url", ""))
	if subHost == "" || panelHost == "" || subHost == panelHost {
		return ""
	}
	return subHost
}

var reservedSubTokens = map[string]bool{
	// backend routes
	"api": true, "s": true, "dl": true, "status": true, "healthz": true, "install.sh": true,
	// frontend SPA top-level routes
	"login": true, "offline": true, "servers": true, "routes": true, "topology": true,
	"latency": true, "subscription": true, "updates": true, "alerts": true, "tools": true, "settings": true,
	// static & PWA assets
	"assets": true, "index.html": true, "favicon.ico": true, "favicon.svg": true,
	"sw.js": true, "registersw.js": true, "manifest.webmanifest": true, "robots.txt": true,
	"apple-touch-icon.png": true, "pwa-192.png": true, "pwa-512.png": true, "pwa-maskable-512.png": true,
}

func adminFirstSegment(adminPath string) string {
	trimmed := strings.Trim(strings.TrimSpace(adminPath), "/")
	if trimmed == "" {
		return ""
	}
	seg, _, _ := strings.Cut(trimmed, "/")
	return strings.ToLower(seg)
}

// ValidateSubToken validates a custom subscription path suffix.
func (p *Panel) ValidateSubToken(raw string) (string, error) {
	tok := strings.TrimSpace(raw)
	if tok == "" {
		return "", errors.New("订阅后缀不能为空")
	}
	if len(tok) > 128 {
		return "", errors.New("订阅后缀长度不能超过 128 个字符")
	}
	if tok[0] == '.' {
		return "", errors.New("订阅后缀不能以 . 开头")
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return "", errors.New("订阅后缀只能包含字母、数字、-、_ 和 .")
		}
	}
	lower := strings.ToLower(tok)
	if reservedSubTokens[lower] || strings.HasPrefix(lower, "workbox-") || strings.HasSuffix(lower, ".webmanifest") {
		return "", fmt.Errorf("订阅后缀「%s」为系统保留路径，请换一个", tok)
	}
	if adminSeg := adminFirstSegment(p.St.Get("admin_path", "")); adminSeg != "" && lower == adminSeg {
		return "", errors.New("订阅后缀与当前后台路径冲突，请换一个")
	}
	return tok, nil
}

func (p *Panel) subTokenTaken(tok string, excludeID uint) bool {
	var n int64
	q := p.St.DB.Model(&store.SubToken{}).Where("token_hash = ?", secret.Hash(tok))
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	q.Count(&n)
	if n > 0 {
		return true
	}
	// Case-insensitive uniqueness check against stored tokens.
	var all []store.SubToken
	q2 := p.St.DB.Select("id, token_enc")
	if excludeID > 0 {
		q2 = q2.Where("id <> ?", excludeID)
	}
	q2.Find(&all)
	for _, t := range all {
		if plain, err := p.Box.Open(t.TokenEnc); err == nil && strings.EqualFold(plain, tok) {
			return true
		}
	}
	return false
}

// HasSubToken reports whether any subscription token (enabled or disabled)
// matches tok (exact match). Used by the router to avoid falling through
// disabled tokens to the SPA.
func (p *Panel) HasSubToken(tok string) bool {
	if tok == "" {
		return false
	}
	var n int64
	p.St.R().Model(&store.SubToken{}).Where("token_hash = ?", secret.Hash(tok)).Count(&n)
	return n > 0
}

// HasSubTokenFold reports whether any subscription token matches tok
// case-insensitively (used to prevent admin_path collisions).
func (p *Panel) HasSubTokenFold(tok string) bool {
	if tok == "" {
		return false
	}
	return p.subTokenTaken(tok, 0)
}

func (p *Panel) SubTokens() []SubTokenView {
	db := p.St.R()
	var ts []store.SubToken
	db.Order("id").Find(&ts)
	counts := map[uint]int64{}
	if len(ts) > 0 {
		var rows []struct {
			TokenID uint
			N       int64
		}
		db.Model(&store.SubAccessLog{}).
			Select("token_id, COUNT(*) AS n").
			Where("ts > ?", time.Now().AddDate(0, 0, -7)).
			Group("token_id").Scan(&rows)
		for _, r := range rows {
			counts[r.TokenID] = r.N
		}
	}
	base := p.SubBase()
	out := make([]SubTokenView, 0, len(ts))
	for _, t := range ts {
		v := SubTokenView{SubToken: t, Accesses: counts[t.ID]}
		if tok, err := p.Box.Open(t.TokenEnc); err == nil {
			v.Token = tok
			v.URL = base + "/" + tok
		}
		out = append(out, v)
	}
	return out
}

func validateSubTarget(target string) error {
	if target != "" && target != "mihomo" && target != "singbox" && target != "base64" {
		return errors.New("格式只能是 mihomo / singbox / base64")
	}
	return nil
}

// parseRouteIDSpec parses a comma-separated list of route IDs.
// nil means unset / all; a non-nil empty slice means a non-empty spec
// contained no valid positive IDs.
func parseRouteIDSpec(spec string) []uint {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "all" {
		return nil
	}
	ids := []uint{}
	seen := map[uint]bool{}
	for _, s := range strings.Split(spec, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			id := uint(n)
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// normalizeSubRoutes validates and canonicalizes a per-token route ID list.
// Empty string or "all" normalizes to "" (follow the global sub_routes setting).
func normalizeSubRoutes(raw string) (string, error) {
	spec := strings.TrimSpace(raw)
	if spec == "" || spec == "all" {
		return "", nil
	}
	var parts []string
	seen := map[uint]bool{}
	for _, s := range strings.Split(spec, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return "", fmt.Errorf("无效的线路 ID：%s", s)
		}
		id := uint(n)
		if !seen[id] {
			seen[id] = true
			parts = append(parts, strconv.Itoa(n))
		}
	}
	return strings.Join(parts, ","), nil
}

func (p *Panel) CreateSubToken(name, target, customToken, routes string) (*store.SubToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("请填写设备名称")
	}
	if err := validateSubTarget(target); err != nil {
		return nil, err
	}
	normRoutes, err := normalizeSubRoutes(routes)
	if err != nil {
		return nil, err
	}
	var tok string
	if strings.TrimSpace(customToken) != "" {
		var err error
		tok, err = p.ValidateSubToken(customToken)
		if err != nil {
			return nil, err
		}
		if p.subTokenTaken(tok, 0) {
			return nil, errors.New("该订阅后缀已被其他设备使用")
		}
	} else {
		tok = secret.RandomToken(24)
	}
	enc := p.Box.Seal(tok)
	t := &store.SubToken{Name: name, TokenEnc: enc, TokenHash: secret.Hash(tok), TargetDefault: target, Routes: normRoutes, Enabled: true, CreatedAt: time.Now()}
	return t, p.St.DB.Create(t).Error
}

func (p *Panel) UpdateSubToken(id uint, name, target string, enabled bool, customToken, routes *string) error {
	var existing store.SubToken
	if err := p.St.DB.First(&existing, id).Error; err != nil {
		return errors.New("设备订阅不存在")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请填写设备名称")
	}
	if err := validateSubTarget(target); err != nil {
		return err
	}
	upd := map[string]any{"name": name, "target_default": target, "enabled": enabled}
	if routes != nil {
		normRoutes, err := normalizeSubRoutes(*routes)
		if err != nil {
			return err
		}
		upd["routes"] = normRoutes
	}
	if customToken != nil {
		tok, err := p.ValidateSubToken(*customToken)
		if err != nil {
			return err
		}
		if p.subTokenTaken(tok, id) {
			return errors.New("该订阅后缀已被其他设备使用")
		}
		upd["token_enc"] = p.Box.Seal(tok)
		upd["token_hash"] = secret.Hash(tok)
	}
	return p.St.DB.Model(&store.SubToken{}).Where("id = ?", id).Updates(upd).Error
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
		XrayVersion: entry.XrayVersion,
	}
}

func VLESSLink(n sub.Node) string { return sub.Link(n) }

// SubRouteIDs parses sub_routes: the global default route IDs picked for
// subscriptions. nil means no picking (every enabled in_sub route goes in).
func (p *Panel) SubRouteIDs() []uint {
	return parseRouteIDSpec(p.St.Get("sub_routes", ""))
}

// effectiveRouteIDs returns the route IDs for token t, falling back to the
// global sub_routes setting when t is nil or has no custom routes set.
func (p *Panel) effectiveRouteIDs(t *store.SubToken) []uint {
	if t != nil {
		if ids := parseRouteIDSpec(t.Routes); ids != nil {
			return ids
		}
	}
	return p.SubRouteIDs()
}

// SubNodes lists the routes that go into global default subscriptions.
func (p *Panel) SubNodes() []sub.Node {
	return p.SubNodesFor(nil)
}

// SubNodesFor lists the routes that go into subscriptions for token t
// (or global default when t is nil).
func (p *Panel) SubNodesFor(t *store.SubToken) []sub.Node {
	db := p.St.R()
	var rs []store.Route
	q := db.Where("enabled = ? AND in_sub = ?", true, true)
	if ids := p.effectiveRouteIDs(t); ids != nil {
		if len(ids) == 0 {
			return nil
		}
		q = q.Where("id IN ?", ids)
	}
	q.Order("sort, id").Find(&rs)
	var svs []store.Server
	db.Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	db.Find(&ris)
	riBy := map[uint]*store.RealityInbound{}
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	exBlocked := p.St.GetBool("sub_exclude_blocked", true)
	exOffline := p.St.GetBool("sub_exclude_offline", false)
	tpl := p.St.Get("sub_name_tpl", "{flag} {name}")
	seen := map[string]int{}
	for name := range sub.ReservedNames() {
		seen[name] = 1
	}
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
		seen[sub.RegionGroupName(n.RegionCode)] = 1
		base := n.Name
		if base == "" {
			base = "node"
		}
		cand := base
		for k := 1; seen[cand] > 0; {
			k++
			cand = fmt.Sprintf("%s %d", base, k)
		}
		n.Name = cand
		seen[cand]++
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
	hits   map[uint][]time.Time
	ipHits map[string][]time.Time
}{
	hits:   map[uint][]time.Time{},
	ipHits: map[string][]time.Time{},
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

func allowSubIP(ip string) bool {
	key := subIPRateKey(ip)
	if key == "" {
		return true
	}
	subLimiter.Lock()
	defer subLimiter.Unlock()
	now := time.Now()
	if len(subLimiter.ipHits) >= 256 {
		for k, ts := range subLimiter.ipHits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Minute {
				delete(subLimiter.ipHits, k)
			}
		}
	}
	if _, exists := subLimiter.ipHits[key]; !exists && len(subLimiter.ipHits) >= subMaxTrackedIPs {
		var oldestKey string
		var oldest time.Time
		for k, ts := range subLimiter.ipHits {
			t := time.Time{}
			if len(ts) > 0 {
				t = ts[len(ts)-1]
			}
			if oldestKey == "" || t.Before(oldest) {
				oldestKey, oldest = k, t
			}
		}
		if oldestKey != "" {
			delete(subLimiter.ipHits, oldestKey)
		}
	}
	var keep []time.Time
	for _, t := range subLimiter.ipHits[key] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= subPerIPPerMin {
		subLimiter.ipHits[key] = keep
		return false
	}
	subLimiter.ipHits[key] = append(keep, now)
	return true
}

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
	if len(keep) >= subPerTokenPerMin {
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
	if !allowSubIP(ip) {
		return nil, ErrSubRateLimited
	}
	var t store.SubToken
	if err := p.St.R().First(&t, "token_hash = ?", secret.Hash(token)).Error; err != nil || !t.Enabled {
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

// PreviewSub renders the global default subscription without logging or rate
// limiting (admin UI).
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

// formatProfileTitle encodes the subscription display name for the
// Profile-Title header (plain ASCII when safe, or "base64:<std-b64>" for UTF-8
// names as expected by sing-box / Hiddify / v2rayN / Clash Verge / FlClash /
// Mihomo Party / Shadowrocket).
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

// formatContentDisposition emits both filename="..." and RFC 5987
// filename*=UTF-8''... using percent-escaped ASCII so clients that only parse
// filename= as well as RFC 5987 clients decode non-ASCII names cleanly.
func formatContentDisposition(name string) string {
	esc := urlPathEscape(name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, esc, esc)
}

// subRouteServerIDsFor returns the unique server IDs (entry, landing, and
// backup landings) used by routes currently included for token t (or global
// default when t is nil).
func (p *Panel) subRouteServerIDsFor(t *store.SubToken) []uint {
	db := p.St.R()
	var rs []store.Route
	q := db.Where("enabled = ? AND in_sub = ?", true, true)
	if ids := p.effectiveRouteIDs(t); ids != nil {
		if len(ids) == 0 {
			return nil
		}
		q = q.Where("id IN ?", ids)
	}
	q.Find(&rs)
	if len(rs) == 0 {
		return nil
	}
	var svs []store.Server
	db.Where("mode = ?", "node").Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	db.Find(&ris)
	riBy := map[uint]*store.RealityInbound{}
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	exBlocked := p.St.GetBool("sub_exclude_blocked", true)
	exOffline := p.St.GetBool("sub_exclude_offline", false)

	seen := map[uint]bool{}
	var ids []uint
	add := func(id uint) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for i := range rs {
		r := &rs[i]
		e, ri := byID[r.EntryServerID], riBy[r.EntryServerID]
		if e == nil || ri == nil || serverAddr(e) == "" && ri.Address == "" {
			continue
		}
		if exBlocked && e.Blocked || exOffline && e.Status == "offline" {
			continue
		}
		add(r.EntryServerID)
		if r.Type == "chain" {
			add(r.LandingServerID)
			for _, b := range parseIDs(r.BackupIDs) {
				add(b)
			}
		}
	}
	return ids
}

// subUserinfo sums the billing-cycle usage of node servers for global default
// subscriptions. Probe-only servers are never included in aggregate totals.
func (p *Panel) subUserinfo() string {
	return p.subUserinfoFor(nil)
}

// subUserinfoFor sums the billing-cycle usage of node servers for token t
// (all node servers, only servers used by routes included for t, or the single
// server set in sub_userinfo). When route selection is active (either on token
// t or via global sub_routes), "all" automatically scopes to the node servers
// used by those selected routes.
func (p *Panel) subUserinfoFor(t *store.SubToken) string {
	mode := p.St.Get("sub_userinfo", "all")
	if mode == "none" {
		return ""
	}
	db := p.St.R()
	var svs []store.Server
	switch {
	case mode == "sub" || (mode == "all" && p.effectiveRouteIDs(t) != nil):
		if ids := p.subRouteServerIDsFor(t); len(ids) > 0 {
			db.Where("mode = ? AND id IN ?", "node", ids).Find(&svs)
		}
	default:
		if id, err := strconv.Atoi(mode); err == nil {
			db.Where("id = ?", id).Find(&svs)
		} else {
			db.Where("mode = ?", "node").Find(&svs)
		}
	}
	usageBy := p.CycleUsageMap(svs)
	var up, down, total uint64
	var expire int64
	for i := range svs {
		u := usageBy[svs[i].ID]
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
