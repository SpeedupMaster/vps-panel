package service

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

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
	return p.ensureReality(serverID, "", "")
}

func (p *Panel) ensureReality(serverID uint, target, serverNames string) (*store.RealityInbound, error) {
	var in store.RealityInbound
	if err := p.St.DB.First(&in, "server_id = ?", serverID).Error; err == nil {
		return &in, nil
	}
	if target == "" {
		var err error
		if target, serverNames, err = realityTarget(p.St.Get("reality_default_target", "www.microsoft.com"), ""); err != nil {
			return nil, err
		}
	}
	priv, pub := NewX25519()
	enc := p.Box.Seal(priv)
	in = store.RealityInbound{
		ServerID: serverID, Port: 443, Target: target, ServerNames: serverNames,
		PrivateKeyEnc: enc, PublicKey: pub, ShortIDs: NewShortID(), Fingerprint: "chrome",
	}
	return &in, p.St.DB.Create(&in).Error
}

var sniRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

func realityTarget(target, serverNames string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("目标站不能为空")
	}
	if strings.Contains(target, "/") {
		return "", "", errors.New("目标站填 host:port，例如 www.example.com:443，不要带 https:// 或路径")
	}
	if !strings.Contains(target, ":") {
		target += ":443"
	}
	names := splitList(serverNames)
	if len(names) == 0 {
		host, _, _ := strings.Cut(target, ":")
		names = []string{host}
	}
	for _, n := range names {
		if !sniRe.MatchString(n) {
			return "", "", fmt.Errorf("SNI %q 不是有效的域名", n)
		}
	}
	return target, strings.Join(names, ","), nil
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
	if in.Target, in.ServerNames, err = realityTarget(in.Target, in.ServerNames); err != nil {
		return err
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
	r.Port, r.Target, r.ServerNames, r.ShortIDs, r.Address, r.Fingerprint = in.Port, in.Target, in.ServerNames, strings.Join(splitList(in.ShortIDs), ","), strings.TrimSpace(in.Address), in.Fingerprint
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
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		if methodChanged { // key lengths differ: regenerate server and user keys
			l.ServerKeyEnc = p.Box.Seal(SSKey(l.Method))
			var users []store.LandingUser
			if err := tx.Where("landing_server_id = ?", serverID).Find(&users).Error; err != nil {
				return err
			}
			for i := range users {
				users[i].UserKeyEnc = p.Box.Seal(SSKey(l.Method))
				if err := tx.Save(&users[i]).Error; err != nil {
					return err
				}
			}
		}
		return tx.Save(l).Error
	}); err != nil {
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

// pruneOrphanLandingUsers removes LandingUser rows for entries that no longer
// have any enabled chain route targeting landingID.
func (p *Panel) pruneOrphanLandingUsers(landingID uint) {
	active := p.entriesOfLanding(landingID)
	if len(active) == 0 {
		p.St.DB.Delete(&store.LandingUser{}, "landing_server_id = ?", landingID)
		return
	}
	p.St.DB.Where("landing_server_id = ? AND entry_server_id NOT IN ?", landingID, active).Delete(&store.LandingUser{})
}

// ---------- server xray details ----------

type LandingUserView struct {
	ID        uint   `json:"id"`
	Email     string `json:"email"`
	EntryID   uint   `json:"entry_server_id"`
	EntryName string `json:"entry_name"`
	Up        uint64 `json:"month_up"`
	Down      uint64 `json:"month_down"`
}

type ServerXrayDetails struct {
	Server          *store.Server
	Reality         *store.RealityInbound
	Landing         *store.LandingInbound
	LandingUsers    []LandingUserView
	LandingFirewall bool
	LatestConfig    *store.ConfigVersion
}

// GetServerXray loads a server's inbound configuration, landing users, and latest config summary.
func (p *Panel) GetServerXray(id uint, since time.Time) (*ServerXrayDetails, error) {
	var sv store.Server
	if err := p.St.R().First(&sv, id).Error; err != nil {
		return nil, ErrNotFound
	}
	out := &ServerXrayDetails{Server: &sv}
	var ri store.RealityInbound
	if p.St.R().First(&ri, "server_id = ?", id).Error == nil {
		out.Reality = &ri
	}
	var li store.LandingInbound
	if p.St.R().First(&li, "server_id = ?", id).Error == nil {
		out.Landing = &li
		var users []store.LandingUser
		p.St.R().Where("landing_server_id = ?", id).Find(&users)
		prefix := strconv.FormatUint(uint64(id), 10) + ":"
		keys := make([]string, len(users))
		for i, u := range users {
			keys[i] = prefix + u.Email
		}
		sums := p.SumMany("landing_user", keys, since)
		for i, u := range users {
			t := sums[keys[i]]
			out.LandingUsers = append(out.LandingUsers, LandingUserView{
				ID: u.ID, Email: u.Email, EntryID: u.EntryServerID,
				EntryName: p.ServerName(u.EntryServerID), Up: t[0], Down: t[1],
			})
		}
		out.LandingFirewall = p.St.GetBool("landing_firewall", true)
	}
	var latest store.ConfigVersion
	if p.St.R().Where("server_id = ?", id).Order("version DESC").Select("id, version, reason, applied, created_at").First(&latest).Error == nil {
		out.LatestConfig = &latest
	}
	return out, nil
}
