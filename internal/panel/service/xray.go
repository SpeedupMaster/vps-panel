package service

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

// ---------- key material ----------

// NewX25519 returns a Reality key pair (base64url, no padding, as xray x25519).
func NewX25519() (priv, pub string) {
	k, _ := ecdh.X25519().GenerateKey(rand.Reader)
	return base64.RawURLEncoding.EncodeToString(k.Bytes()), base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
}

// RealityPublicKey derives the public key from a private key.
func RealityPublicKey(priv string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func NewShortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// SSKey returns a base64 key of the length the SS2022 method needs.
func SSKey(method string) string {
	n := 16
	if strings.Contains(method, "256") {
		n = 32
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func randomPort(lo, hi int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(hi-lo)))
	return lo + int(n.Int64())
}

// ---------- inbounds ----------

// EnsureReality creates the entry inbound with defaults if missing.
func (p *Panel) EnsureReality(serverID uint) (*store.RealityInbound, error) {
	var in store.RealityInbound
	if err := p.St.DB.First(&in, "server_id = ?", serverID).Error; err == nil {
		return &in, nil
	}
	priv, pub := NewX25519()
	enc := p.Box.Seal(priv)
	target := p.St.Get("reality_default_target", "www.microsoft.com")
	in = store.RealityInbound{
		ServerID: serverID, Port: 443, Target: target + ":443", ServerNames: target,
		PrivateKeyEnc: enc, PublicKey: pub, ShortIDs: NewShortID(), Fingerprint: "chrome",
	}
	return &in, p.St.DB.Create(&in).Error
}

type RealityInput struct {
	Port        int    `json:"port"`
	Target      string `json:"target"`
	ServerNames string `json:"server_names"`
	ShortIDs    string `json:"short_ids"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	RegenKeys   bool   `json:"regen_keys"`
}

func (p *Panel) UpdateReality(serverID uint, in RealityInput) error {
	r, err := p.EnsureReality(serverID)
	if err != nil {
		return err
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("端口无效")
	}
	in.Target = strings.TrimSpace(in.Target)
	if in.Target == "" {
		return errors.New("目标站不能为空")
	}
	if !strings.Contains(in.Target, ":") {
		in.Target += ":443"
	}
	if strings.TrimSpace(in.ServerNames) == "" {
		in.ServerNames, _, _ = strings.Cut(in.Target, ":")
	}
	for _, sid := range splitList(in.ShortIDs) {
		if len(sid) > 16 || len(sid)%2 != 0 {
			return fmt.Errorf("shortId %q 必须是 0–16 位偶数长度的十六进制", sid)
		}
		if _, err := hex.DecodeString(sid); err != nil {
			return fmt.Errorf("shortId %q 不是十六进制", sid)
		}
	}
	if in.ShortIDs == "" {
		in.ShortIDs = NewShortID()
	}
	if in.Fingerprint == "" {
		in.Fingerprint = "chrome"
	}
	r.Port, r.Target, r.ServerNames, r.ShortIDs, r.Address, r.Fingerprint = in.Port, in.Target, strings.Join(splitList(in.ServerNames), ","), strings.Join(splitList(in.ShortIDs), ","), strings.TrimSpace(in.Address), in.Fingerprint
	if in.RegenKeys {
		priv, pub := NewX25519()
		r.PrivateKeyEnc = p.Box.Seal(priv)
		r.PublicKey = pub
	}
	if err := p.St.DB.Save(r).Error; err != nil {
		return err
	}
	_, err = p.Deploy(serverID, "修改 Reality 入站", nil)
	return err
}

func (p *Panel) EnsureLanding(serverID uint) (*store.LandingInbound, error) {
	var in store.LandingInbound
	if err := p.St.DB.First(&in, "server_id = ?", serverID).Error; err == nil {
		return &in, nil
	}
	method := "2022-blake3-aes-128-gcm"
	enc := p.Box.Seal(SSKey(method))
	in = store.LandingInbound{ServerID: serverID, Port: randomPort(20000, 60000), Method: method, ServerKeyEnc: enc}
	return &in, p.St.DB.Create(&in).Error
}

type LandingInput struct {
	Port    int    `json:"port"`
	Method  string `json:"method"`
	Address string `json:"address"`
}

func (p *Panel) UpdateLanding(serverID uint, in LandingInput) error {
	l, err := p.EnsureLanding(serverID)
	if err != nil {
		return err
	}
	if in.Port < 1024 || in.Port > 65535 {
		return errors.New("端口需在 1024–65535")
	}
	if in.Method != "2022-blake3-aes-128-gcm" && in.Method != "2022-blake3-aes-256-gcm" {
		return errors.New("不支持的加密方式")
	}
	in.Address = strings.TrimSpace(in.Address)
	if in.Address != "" && !validHost(in.Address) {
		return errors.New("连接地址需是 IP 或域名")
	}
	methodChanged := in.Method != l.Method
	l.Port, l.Method, l.Address = in.Port, in.Method, in.Address
	if methodChanged { // key lengths differ: regenerate server and user keys
		l.ServerKeyEnc = p.Box.Seal(SSKey(l.Method))
		var users []store.LandingUser
		p.St.DB.Where("landing_server_id = ?", serverID).Find(&users)
		for i := range users {
			users[i].UserKeyEnc = p.Box.Seal(SSKey(l.Method))
			p.St.DB.Save(&users[i])
		}
	}
	if err := p.St.DB.Save(l).Error; err != nil {
		return err
	}
	return p.redeployLandingAndEntries(serverID, "修改落地入站")
}

// landingUser returns (creating if needed) the SS user an entry uses on a landing.
func (p *Panel) landingUser(landingID, entryID uint) (*store.LandingUser, bool, error) {
	var u store.LandingUser
	if err := p.St.DB.First(&u, "landing_server_id = ? AND entry_server_id = ?", landingID, entryID).Error; err == nil {
		return &u, false, nil
	}
	l, err := p.EnsureLanding(landingID)
	if err != nil {
		return nil, false, err
	}
	enc := p.Box.Seal(SSKey(l.Method))
	u = store.LandingUser{LandingServerID: landingID, EntryServerID: entryID, UserKeyEnc: enc, Email: fmt.Sprintf("from-%d", entryID), CreatedAt: time.Now()}
	return &u, true, p.St.DB.Create(&u).Error
}

// ---------- config generation ----------

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

// landingAddr is where entries reach a landing: the configured connection
// address, else its public IP.
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

// usedLandings are the landings referenced by enabled chain routes of an entry.
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

// ---------- deploy / versions ----------

func (p *Panel) latestVersion(serverID uint) *store.ConfigVersion {
	var v store.ConfigVersion
	if err := p.St.DB.Where("server_id = ?", serverID).Order("version DESC").First(&v).Error; err != nil {
		return nil
	}
	return &v
}

// Deploy renders the config, records a version when it changed, and pushes
// it. With op set (single user add/remove), the agent tries a hot update via
// the Xray API first. Returns the task id (0 when nothing was sent).
func (p *Panel) Deploy(serverID uint, reason string, op *proto.XrayOp) (uint64, error) {
	raw, fw, err := p.BuildConfig(serverID)
	if err != nil {
		return 0, err
	}
	pretty := string(xrayconf.Pretty(raw))
	last := p.latestVersion(serverID)
	ver := 1
	if last != nil {
		ver = last.Version + 1
	}
	if last == nil || last.Config != pretty {
		prev := ""
		if last != nil {
			prev = last.Config
		}
		cv := &store.ConfigVersion{ServerID: serverID, Version: ver, Config: pretty, Diff: LineDiff(prev, pretty), Reason: reason, CreatedAt: time.Now()}
		if err := p.St.DB.Create(cv).Error; err != nil {
			return 0, err
		}
		p.pruneVersions(serverID)
	} else {
		ver = last.Version
		if last.Applied && op == nil {
			// Config unchanged; the landing whitelist may still differ.
			if fw != nil && p.Hub.Online(serverID) {
				key := fmt.Sprintf("fw_hash_%d", serverID)
				fb, _ := json.Marshal(fw)
				if h := xrayconf.Hash(fb); h != p.St.Get(key, "") {
					if p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{Firewall: fw}}) == nil {
						p.St.Set(key, h)
					}
				}
			}
			return 0, nil
		}
	}
	if !p.Hub.Online(serverID) {
		return 0, nil // synced from onXrayState when the agent comes back
	}
	var sv store.Server
	p.St.DB.Select("xray_version").First(&sv, serverID)
	if sv.XrayVersion == "" {
		return 0, nil // not installed yet
	}
	if op != nil {
		op.FullConfig, op.Version = raw, ver
		t, err := p.newInternalTask(serverID, "xray_op", ver)
		if err != nil {
			return 0, err
		}
		op.TaskID = t.ID
		if err := p.Hub.Send(serverID, &proto.PanelMessage{XrayOp: op}); err != nil {
			return 0, err
		}
		if fw != nil { // landing whitelist may have changed too
			_ = p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{Firewall: fw}})
		}
		return t.ID, nil
	}
	return p.pushApply(serverID, ver, raw, fw, false)
}

func compact(s string) []byte {
	var b bytes.Buffer
	if json.Compact(&b, []byte(s)) != nil {
		return []byte(s)
	}
	return b.Bytes()
}

func (p *Panel) pushApply(serverID uint, ver int, raw []byte, fw *proto.FirewallPolicy, restart bool) (uint64, error) {
	t, err := p.newInternalTask(serverID, "apply_config", ver)
	if err != nil {
		return 0, err
	}
	err = p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{TaskID: t.ID, Version: ver, Config: raw, Firewall: fw, Restart: restart}})
	if err != nil {
		p.finishTask(t, "failed", "发送失败："+err.Error())
		return t.ID, err
	}
	if fw != nil {
		fb, _ := json.Marshal(fw)
		p.St.Set(fmt.Sprintf("fw_hash_%d", serverID), xrayconf.Hash(fb))
	}
	return t.ID, nil
}

// newInternalTask records a task the agent answers via TaskResult but which
// is not dispatched as a proto.Task (apply_config / xray_op).
func (p *Panel) newInternalTask(serverID uint, typ string, ver int) (*store.Task, error) {
	exp := time.Now().Add(10 * time.Minute)
	t := &store.Task{
		ID: uint64(time.Now().UnixNano()), ServerID: serverID, Type: typ, Args: fmt.Sprintf(`{"version":"%d"}`, ver),
		Status: "sent", Timeout: 120, ExpireAt: &exp, CreatedAt: time.Now(),
	}
	return t, p.St.DB.Create(t).Error
}

func (p *Panel) pruneVersions(serverID uint) {
	keep := p.St.GetInt("config_versions_keep", 50)
	p.St.DB.Exec(`DELETE FROM config_versions WHERE server_id = ? AND id NOT IN
		(SELECT id FROM config_versions WHERE server_id = ? ORDER BY version DESC LIMIT ?)`, serverID, serverID, keep)
}

// Rollback re-applies an older version as a new version.
func (p *Panel) Rollback(serverID uint, versionID uint) (uint64, error) {
	var old store.ConfigVersion
	if err := p.St.DB.First(&old, "id = ? AND server_id = ?", versionID, serverID).Error; err != nil {
		return 0, errors.New("版本不存在")
	}
	last := p.latestVersion(serverID)
	cv := &store.ConfigVersion{ServerID: serverID, Version: last.Version + 1, Config: old.Config, Diff: LineDiff(last.Config, old.Config),
		Reason: fmt.Sprintf("回滚到 v%d", old.Version), CreatedAt: time.Now()}
	if err := p.St.DB.Create(cv).Error; err != nil {
		return 0, err
	}
	if !p.Hub.Online(serverID) {
		return 0, errors.New("Agent 不在线，上线后会自动同步")
	}
	_, fw, _ := p.BuildConfig(serverID)
	return p.pushApply(serverID, cv.Version, compact(old.Config), fw, true)
}

// redeployLandingAndEntries pushes a landing and every entry chained to it.
func (p *Panel) redeployLandingAndEntries(landingID uint, reason string) error {
	if _, err := p.Deploy(landingID, reason, nil); err != nil {
		return err
	}
	for _, e := range p.entriesOfLanding(landingID) {
		if _, err := p.Deploy(e, reason, nil); err != nil {
			return err
		}
	}
	return nil
}

// onXrayState stores state and keeps the node in sync with the panel:
// install Xray if missing, push the latest config if the hash differs.
func (p *Panel) onXrayState(serverID uint, s *proto.XrayState) {
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return
	}
	p.St.DB.Model(&sv).Updates(map[string]any{
		"xray_version": s.Version, "xray_running": s.Running, "xray_config_ver": s.ConfigVer, "xray_drift": s.Drift,
	})
	sv.XrayVersion = s.Version
	if sv.Mode != "node" {
		return
	}
	if !s.Installed {
		if p.St.GetBool("auto_install_xray", true) && !p.hasActiveTask(serverID, proto.TaskInstallXray) {
			p.CreateTask(serverID, proto.TaskInstallXray, p.xraySourceArgs(sv.XrayLockedVersion), nil, 600, "")
		}
		return
	}
	if p.hasActiveTask(serverID, "apply_config") || p.hasActiveTask(serverID, "xray_op") {
		return
	}
	last := p.latestVersion(serverID)
	if last == nil || !s.Drift && s.ConfigHash == xrayconf.Hash([]byte(last.Config)) {
		if last == nil && (sv.CapReality || sv.CapLanding) {
			go p.Deploy(serverID, "首次部署", nil)
		} else if last != nil && !last.Applied {
			p.markApplied(serverID, last.Version)
		}
		return
	}
	if s.Drift && !p.St.GetBool("drift_autofix", true) {
		return
	}
	// A push that just failed would fail again: wait before retrying on our
	// own (manual deploys still go through) instead of looping on every
	// state report.
	if p.recentFailedApply(serverID, 10*time.Minute) {
		return
	}
	// Re-render in case the Xray version changed (dest vs target), then push.
	go func() {
		if _, err := p.Deploy(serverID, "同步配置", nil); err == nil {
			if l := p.latestVersion(serverID); l != nil && xrayconf.Hash([]byte(l.Config)) != s.ConfigHash && !p.hasActiveTask(serverID, "apply_config") {
				_, fw, _ := p.BuildConfig(serverID)
				p.pushApply(serverID, l.Version, compact(l.Config), fw, false)
			}
		}
	}()
}

func (p *Panel) hasActiveTask(serverID uint, typ string) bool {
	var n int64
	p.St.DB.Model(&store.Task{}).Where("server_id = ? AND type = ? AND status IN ? AND created_at > ?",
		serverID, typ, []string{"pending", "sent", "running"}, time.Now().Add(-15*time.Minute)).Count(&n)
	return n > 0
}

func (p *Panel) recentFailedApply(serverID uint, within time.Duration) bool {
	var t store.Task
	err := p.St.DB.Where("server_id = ? AND type IN ? AND finished_at IS NOT NULL", serverID, []string{"apply_config", "xray_op"}).
		Order("id DESC").Select("status, finished_at").First(&t).Error
	return err == nil && t.Status != "success" && t.FinishedAt != nil && time.Since(*t.FinishedAt) < within
}

func (p *Panel) markApplied(serverID uint, ver int) {
	p.St.DB.Model(&store.ConfigVersion{}).Where("server_id = ? AND version = ?", serverID, ver).Update("applied", true)
	p.St.DB.Model(&store.Server{}).Where("id = ?", serverID).Updates(map[string]any{"xray_config_ver": ver, "xray_drift": false})
}

// xraySourceArgs picks the version and optional download relay.
func (p *Panel) xraySourceArgs(locked string) map[string]string {
	v := locked
	if v == "" {
		v = p.St.Get("xray_pinned_version", "latest")
	}
	if v == "latest" { // resolve from the release cache so the relay can serve it
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

// onIPChanged updates landing whitelists and subscriptions when an entry or
// landing machine gets a new IP.
func (p *Panel) onIPChanged(sv *store.Server, oldIP, newIP string) {
	p.Alert.Info("ip_changed", sv.ID, fmt.Sprintf("%s 的 IP 由 %s 变为 %s，已同步落地白名单和订阅", sv.Name, oldIP, newIP))
	p.Audit("server", "ip_changed", sv.Name, oldIP+" → "+newIP, "")
	go func() {
		time.Sleep(2 * time.Second) // let OnConnect store the new IP first
		if sv.CapReality {
			var rs []store.Route
			p.St.DB.Where("entry_server_id = ? AND type = ?", sv.ID, "chain").Find(&rs)
			done := map[uint]bool{}
			for _, r := range rs {
				for _, l := range append([]uint{r.LandingServerID}, parseIDs(r.BackupIDs)...) {
					if !done[l] {
						done[l] = true
						p.Deploy(l, "入口机 IP 变化", nil)
					}
				}
			}
		}
		if sv.CapLanding {
			for _, e := range p.entriesOfLanding(sv.ID) {
				p.Deploy(e, "落地机 IP 变化", nil)
			}
		}
	}()
}

// afterTask reacts to finished tasks.
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

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// ---------- mode changes ----------

// UpgradeToNode turns a probe-only machine into a node and installs Xray.
func (p *Panel) UpgradeToNode(serverID uint, capReality, capLanding bool) error {
	if !capReality && !capLanding {
		return errors.New("至少选择一种能力")
	}
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return err
	}
	if sv.Mode == "node" {
		// capability change only
		if !capReality {
			var n int64
			p.St.DB.Model(&store.Route{}).Where("entry_server_id = ?", serverID).Count(&n)
			if n > 0 {
				return errors.New("还有以这台机器为入口的线路")
			}
		}
		if !capLanding && len(p.entriesOfLanding(serverID)) > 0 {
			return errors.New("还有线路使用这台机器做落地")
		}
		p.St.DB.Model(&sv).Updates(map[string]any{"cap_reality": capReality, "cap_landing": capLanding})
		_, err := p.Deploy(serverID, "修改节点能力", nil)
		return err
	}
	p.St.DB.Model(&sv).Updates(map[string]any{"mode": "node", "cap_reality": capReality, "cap_landing": capLanding})
	if capReality {
		if _, err := p.EnsureReality(serverID); err != nil {
			return err
		}
	}
	if capLanding {
		if _, err := p.EnsureLanding(serverID); err != nil {
			return err
		}
	}
	_, err := p.CreateTask(serverID, proto.TaskInstallXray, p.xraySourceArgs(""), nil, 600, "")
	return err
}

// DowngradeToProbe uninstalls Xray. Routes must be removed first.
func (p *Panel) DowngradeToProbe(serverID uint) error {
	var n int64
	p.St.DB.Model(&store.Route{}).Where("entry_server_id = ? OR landing_server_id = ?", serverID, serverID).Count(&n)
	if n > 0 || len(p.entriesOfLanding(serverID)) > 0 {
		return errors.New("还有线路使用这台机器，请先删除相关线路")
	}
	p.St.DB.Model(&store.Server{}).Where("id = ?", serverID).Updates(map[string]any{"mode": "probe", "cap_reality": false, "cap_landing": false, "xray_version": "", "xray_running": false})
	p.St.DB.Delete(&store.RealityInbound{}, "server_id = ?", serverID)
	p.St.DB.Delete(&store.LandingInbound{}, "server_id = ?", serverID)
	p.St.DB.Delete(&store.LandingUser{}, "landing_server_id = ?", serverID)
	_, err := p.CreateTask(serverID, proto.TaskUninstallXray, map[string]string{"downgrade": "1"}, nil, 120, "")
	return err
}

// RotateAll regenerates every secret clients depend on: route UUIDs,
// Reality keys and shortIds, SS keys. Then redeploys every node.
func (p *Panel) RotateAll() error {
	var rs []store.Route
	p.St.DB.Find(&rs)
	for i := range rs {
		p.St.DB.Model(&rs[i]).Update("uuid", NewUUID())
	}
	var ris []store.RealityInbound
	p.St.DB.Find(&ris)
	for i := range ris {
		priv, pub := NewX25519()
		enc := p.Box.Seal(priv)
		p.St.DB.Model(&ris[i]).Updates(map[string]any{"private_key_enc": enc, "public_key": pub, "short_ids": NewShortID()})
	}
	var lis []store.LandingInbound
	p.St.DB.Find(&lis)
	for i := range lis {
		enc := p.Box.Seal(SSKey(lis[i].Method))
		p.St.DB.Model(&lis[i]).Update("server_key_enc", enc)
		var us []store.LandingUser
		p.St.DB.Where("landing_server_id = ?", lis[i].ServerID).Find(&us)
		for j := range us {
			e := p.Box.Seal(SSKey(lis[i].Method))
			p.St.DB.Model(&us[j]).Update("user_key_enc", e)
		}
	}
	var nodes []store.Server
	p.St.DB.Where("mode = ?", "node").Find(&nodes)
	for _, n := range nodes { // landings first so entries never point at stale keys for long
		if n.CapLanding {
			p.Deploy(n.ID, "一键轮换", nil)
		}
	}
	for _, n := range nodes {
		if !n.CapLanding {
			p.Deploy(n.ID, "一键轮换", nil)
		}
	}
	return nil
}
