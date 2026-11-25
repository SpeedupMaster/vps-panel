package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

// ---------- config generation & firewall ----------

func outTag(landingID uint) string { return fmt.Sprintf("to-%d-", landingID) }

func splitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func parseIDs(s string) []uint {
	var ids []uint
	for _, x := range splitList(s) {
		if v, err := strconv.ParseUint(x, 10, 64); err == nil {
			ids = append(ids, uint(v))
		}
	}
	return ids
}

// privatePeers: entries that reach a landing through a private connection
// address arrive from private source IPs, which the public-IP whitelist would
// drop. Those ranges can't be reached from the internet, so allowing them
// keeps the whitelist meaningful.
func privatePeers(addr string) []string {
	a, err := netip.ParseAddr(addr)
	if err != nil || !(a.IsPrivate() || a.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(a)) {
		return nil
	}
	if a.Is4() {
		return []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10"}
	}
	return []string{"fc00::/7", "fe80::/10"}
}

func landingAddr(sv *store.Server, l *store.LandingInbound) string {
	if l != nil && l.Address != "" {
		return l.Address
	}
	return serverAddr(sv)
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

func validHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	return hostRe.MatchString(s) && !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, ".")
}

func serverAddr(sv *store.Server) string {
	if sv.IPv4 != "" {
		return sv.IPv4
	}
	return sv.IPv6
}

// entryRoutes are the enabled routes of an entry server.
func (p *Panel) entryRoutes(entryID uint) []store.Route {
	var rs []store.Route
	p.St.DB.Where("entry_server_id = ? AND enabled = ?", entryID, true).Order("sort, id").Find(&rs)
	return rs
}

// entriesOfLanding returns entry server ids that route through a landing
// (as primary or backup).
func (p *Panel) entriesOfLanding(landingID uint) []uint {
	var rs []store.Route
	p.St.DB.Where("type = ? AND enabled = ?", "chain", true).Find(&rs)
	seen := map[uint]bool{}
	var ids []uint
	for _, r := range rs {
		hit := r.LandingServerID == landingID
		for _, b := range parseIDs(r.BackupIDs) {
			hit = hit || b == landingID
		}
		if hit && !seen[r.EntryServerID] {
			seen[r.EntryServerID] = true
			ids = append(ids, r.EntryServerID)
		}
	}
	return ids
}

// BuildConfig renders the desired Xray config and firewall for a node.
func (p *Panel) BuildConfig(serverID uint) ([]byte, *proto.FirewallPolicy, error) {
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return nil, nil, err
	}
	if sv.Mode != "node" {
		return nil, nil, errors.New("仅探针模式的机器没有 Xray 配置")
	}
	n := xrayconf.Node{
		XrayVersion: sv.XrayVersion,
		AccessLog:   p.St.GetBool("xray_access_log", false),
		BlockBT:     p.St.GetBool("xray_block_bt", true),
		ProbeURL:    p.St.Get("xray_probe_url", ""),
	}
	var fw *proto.FirewallPolicy

	if sv.CapReality {
		r, err := p.EnsureReality(sv.ID)
		if err != nil {
			return nil, nil, err
		}
		priv, err := p.Box.Open(r.PrivateKeyEnc)
		if err != nil {
			return nil, nil, err
		}
		n.Reality = &xrayconf.Reality{Port: r.Port, Target: r.Target, ServerNames: splitList(r.ServerNames), PrivateKey: priv, ShortIDs: splitList(r.ShortIDs)}
		outs := map[uint]bool{}
		addOut := func(landingID uint) error {
			if outs[landingID] {
				return nil
			}
			var lsv store.Server
			if err := p.St.DB.First(&lsv, landingID).Error; err != nil {
				return fmt.Errorf("落地机 #%d 不存在", landingID)
			}
			l, err := p.EnsureLanding(landingID)
			if err != nil {
				return err
			}
			u, _, err := p.landingUser(landingID, sv.ID)
			if err != nil {
				return err
			}
			sk, err := p.Box.Open(l.ServerKeyEnc)
			if err != nil {
				return err
			}
			uk, err := p.Box.Open(u.UserKeyEnc)
			if err != nil {
				return err
			}
			addr := landingAddr(&lsv, l)
			if addr == "" {
				return fmt.Errorf("落地机 %s 还没有上报 IP", lsv.Name)
			}
			outs[landingID] = true
			n.Outs = append(n.Outs, xrayconf.SSOut{Tag: outTag(landingID) + slug(lsv.Name), Address: addr, Port: l.Port, Method: l.Method, Password: sk + ":" + uk})
			return nil
		}
		for _, rt := range p.entryRoutes(sv.ID) {
			u := xrayconf.User{Email: rt.Email, UUID: rt.UUID, Outbound: "direct"}
			if rt.Type == "chain" {
				if err := addOut(rt.LandingServerID); err != nil {
					return nil, nil, err
				}
				backups := parseIDs(rt.BackupIDs)
				if len(backups) == 0 {
					u.Outbound = n.Outs[indexOut(n.Outs, outTag(rt.LandingServerID))].Tag
				} else {
					sel := []string{outTag(rt.LandingServerID)}
					for _, b := range backups {
						if err := addOut(b); err != nil {
							return nil, nil, err
						}
						sel = append(sel, outTag(b))
					}
					tag := "bal-" + rt.Email
					n.Balancers = append(n.Balancers, xrayconf.Balancer{Tag: tag, Selector: sel, Fallback: n.Outs[indexOut(n.Outs, outTag(rt.LandingServerID))].Tag})
					u.Balancer = tag
				}
			}
			n.Users = append(n.Users, u)
		}
	}
	if sv.CapLanding {
		l, err := p.EnsureLanding(sv.ID)
		if err != nil {
			return nil, nil, err
		}
		sk, err := p.Box.Open(l.ServerKeyEnc)
		if err != nil {
			return nil, nil, err
		}
		n.Landing = &xrayconf.Landing{Port: l.Port, Method: l.Method, ServerKey: sk}
		fw = &proto.FirewallPolicy{Port: l.Port}
		for _, eid := range p.entriesOfLanding(sv.ID) {
			u, _, err := p.landingUser(sv.ID, eid)
			if err != nil {
				return nil, nil, err
			}
			uk, err := p.Box.Open(u.UserKeyEnc)
			if err != nil {
				return nil, nil, err
			}
			n.Landing.Clients = append(n.Landing.Clients, xrayconf.SSClient{Email: u.Email, Password: uk})
			var esv store.Server
			if p.St.DB.First(&esv, eid).Error == nil {
				for _, ip := range []string{esv.IPv4, esv.IPv6} {
					if ip != "" {
						fw.Allowed = append(fw.Allowed, ip)
					}
				}
			}
		}
		fw.Allowed = append(fw.Allowed, privatePeers(l.Address)...)
		if !p.St.GetBool("landing_firewall", true) {
			fw = &proto.FirewallPolicy{} // port 0 removes the table
		}
	}
	b, err := xrayconf.Build(n)
	return b, fw, err
}

func indexOut(outs []xrayconf.SSOut, prefix string) int {
	for i, o := range outs {
		if strings.HasPrefix(o.Tag, prefix) {
			return i
		}
	}
	return 0
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

// syncFirewall pushes a landing whitelist that differs from the last one the
// agent received. An offline agent gets it from onXrayState when it's back.
func (p *Panel) syncFirewall(serverID uint, fw *proto.FirewallPolicy) {
	if fw == nil || !p.Hub.Online(serverID) {
		return
	}
	key := fmt.Sprintf("fw_hash_%d", serverID)
	fb, _ := json.Marshal(fw)
	if h := xrayconf.Hash(fb); h != p.St.Get(key, "") {
		if p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{Firewall: fw}}) == nil {
			_ = p.St.Set(key, h)
		}
	}
}

// RedeployLandings re-renders every landing, e.g. after the whitelist setting changed.
func (p *Panel) RedeployLandings(reason string) {
	var ls []store.LandingInbound
	p.St.DB.Find(&ls)
	for _, l := range ls {
		if _, err := p.Deploy(l.ServerID, reason, nil); err != nil {
			log.Printf("redeploy landing %d: %v", l.ServerID, err)
		}
	}
}

// ---------- config version encryption & queries ----------

const sealedConfigPrefix = "enc:v1:"

func (p *Panel) sealConfigText(plain string) string {
	if plain == "" {
		return ""
	}
	return sealedConfigPrefix + p.Box.Seal(plain)
}

// OpenConfigText decrypts ConfigVersion.Config / Diff stored at rest,
// transparently returning legacy plaintext rows as-is.
func (p *Panel) OpenConfigText(stored string) string {
	if stored == "" {
		return ""
	}
	if strings.HasPrefix(stored, sealedConfigPrefix) {
		if s, err := p.Box.Open(strings.TrimPrefix(stored, sealedConfigPrefix)); err == nil {
			return s
		}
	}
	if !strings.HasPrefix(stored, "{") && !strings.HasPrefix(stored, " ") && !strings.HasPrefix(stored, "+") && !strings.HasPrefix(stored, "-") {
		if s, err := p.Box.Open(stored); err == nil {
			return s
		}
	}
	return stored
}

func (p *Panel) ListConfigVersions(serverID uint, limit int) []store.ConfigVersion {
	if limit <= 0 {
		limit = 100
	}
	var vs []store.ConfigVersion
	p.St.R().Select("id, server_id, version, reason, applied, created_at").
		Where("server_id = ?", serverID).Order("version DESC").Limit(limit).Find(&vs)
	return vs
}

func (p *Panel) GetConfigVersion(serverID uint, vid string) (*store.ConfigVersion, error) {
	var v store.ConfigVersion
	if err := p.St.R().First(&v, "id = ? AND server_id = ?", vid, serverID).Error; err != nil {
		return nil, ErrNotFound
	}
	return &v, nil
}

func (p *Panel) PrevConfigVersion(serverID uint, beforeVersion int) *store.ConfigVersion {
	var prev store.ConfigVersion
	if p.St.R().Where("server_id = ? AND version < ?", serverID, beforeVersion).Order("version DESC").First(&prev).Error != nil {
		return nil
	}
	return &prev
}

func (p *Panel) xraySourceArgs(locked string) map[string]string {
	v := locked
	if v == "" {
		v = p.St.Get("xray_pinned_version", "latest")
	}
	if v == "latest" {
		if l := p.LatestXray(false); l != "" {
			v = l
		}
	}
	args := map[string]string{"version": v}
	if p.St.GetBool("xray_relay", false) && v != "latest" {
		base := strings.TrimRight(p.St.Get("panel_url", ""), "/")
		args["base_url"] = base + "/dl/xray/v" + strings.TrimPrefix(v, "v")
	}
	return args
}

func (p *Panel) onIPChanged(sv *store.Server, oldIP, newIP string) {
	p.Alert.Info("ip_changed", sv.ID, fmt.Sprintf("%s 的 IP 由 %s 变为 %s，已同步落地白名单和订阅", sv.Name, oldIP, newIP))
	p.Audit("server", "ip_changed", sv.Name, oldIP+" → "+newIP, "")
	go func() {
		time.Sleep(2 * time.Second)
		if sv.CapReality {
			var rs []store.Route
			p.St.DB.Where("entry_server_id = ? AND type = ?", sv.ID, "chain").Find(&rs)
			done := map[uint]bool{}
			for _, r := range rs {
				for _, l := range append([]uint{r.LandingServerID}, parseIDs(r.BackupIDs)...) {
					if !done[l] {
						done[l] = true
						_, _ = p.Deploy(l, "入口机 IP 变化", nil)
					}
				}
			}
		}
		if sv.CapLanding {
			for _, e := range p.entriesOfLanding(sv.ID) {
				_, _ = p.Deploy(e, "落地机 IP 变化", nil)
			}
		}
	}()
}

func (p *Panel) afterTask(t *store.Task, r *proto.TaskResult) {
	switch t.Type {
	case "apply_config", "xray_op":
		var a struct{ Version string }
		_ = json.Unmarshal([]byte(t.Args), &a)
		ver, _ := strconv.Atoi(a.Version)
		if r.Status == "success" {
			p.markApplied(t.ServerID, ver)
		} else {
			p.Alert.Info("deploy_failed", t.ServerID, p.ServerName(t.ServerID)+" 配置下发失败："+lastLine(r.Output))
		}
		p.Bus.Publish("xray", map[string]any{"server_id": t.ServerID, "version": ver, "status": r.Status})
	case proto.TaskInstallXray:
		if r.Status == "success" {
			p.Audit("server", "install_xray", p.ServerName(t.ServerID), "Xray 安装完成", "")
		}
	}
	p.afterToolTask(t, r)
}
