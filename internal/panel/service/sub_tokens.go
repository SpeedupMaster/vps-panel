package service

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
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

// DeleteSubToken deletes a token along with its access logs, clears its rate
// limiter bucket, and records an audit entry.
func (p *Panel) DeleteSubToken(id uint, ip string) error {
	var tok store.SubToken
	target := strconv.FormatUint(uint64(id), 10)
	if p.St.DB.First(&tok, id).Error == nil && tok.Name != "" {
		target = tok.Name
	}
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&store.SubAccessLog{}, "token_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&store.SubToken{}, id).Error
	}); err != nil {
		return err
	}
	p.subLimiter().clearToken(id)
	p.Audit("sub", "delete_token", target, "", ip)
	return nil
}

// ---------- templates ----------

func (p *Panel) ListSubTemplates() []store.SubTemplate {
	var ts []store.SubTemplate
	p.St.R().Order("target, id").Find(&ts)
	return ts
}

// SaveSubTemplate validates and atomically persists a subscription template,
// ensuring at most one active template exists per target.
func (p *Panel) SaveSubTemplate(in *store.SubTemplate) error {
	if err := ValidateTemplate(in.Target, in.Content, p.SubNodes()); err != nil {
		return err
	}
	return p.St.DB.Transaction(func(tx *gorm.DB) error {
		if in.Active {
			if err := tx.Model(&store.SubTemplate{}).Where("target = ? AND id <> ?", in.Target, in.ID).Update("active", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(in).Error
	})
}

func (p *Panel) DeleteSubTemplate(id uint) error {
	return p.St.DB.Delete(&store.SubTemplate{}, id).Error
}
