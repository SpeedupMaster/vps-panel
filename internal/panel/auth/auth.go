// Package auth handles the single admin account: password, TOTP, sessions,
// login rate limiting and the access IP allowlist.
package auth

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

const (
	CookieName  = "vp_session"
	sessionTTL  = 7 * 24 * time.Hour
	maxFailures = 5
	lockFor     = 15 * time.Minute
)

var (
	ErrBadCredentials = errors.New("用户名或密码错误")
	ErrNeedTOTP       = errors.New("需要两步验证码")
	ErrBadTOTP        = errors.New("验证码错误")
	ErrLocked         = errors.New("登录失败次数过多，请 15 分钟后再试")
)

type Auth struct {
	st  *store.Store
	box *secret.Box

	mu       sync.Mutex
	failures map[string][]time.Time // ip -> failure times
}

func New(st *store.Store, box *secret.Box) *Auth {
	return &Auth{st: st, box: box, failures: map[string][]time.Time{}}
}

// EnsureAdmin creates the admin account on first start and returns the
// generated password (empty when the account already exists).
func (a *Auth) EnsureAdmin(username, password string) (string, error) {
	var n int64
	a.st.DB.Model(&store.Admin{}).Count(&n)
	if n > 0 {
		return "", nil
	}
	if password == "" {
		password = secret.RandomToken(12)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return password, a.st.DB.Create(&store.Admin{Username: username, PasswordHash: string(hash), PasswordAt: time.Now()}).Error
}

func (a *Auth) Admin() (*store.Admin, error) {
	var ad store.Admin
	err := a.st.DB.First(&ad).Error
	return &ad, err
}

func (a *Auth) locked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().Add(-lockFor)
	var keep []time.Time
	for _, t := range a.failures[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.failures[ip] = keep
	return len(keep) >= maxFailures
}

func (a *Auth) fail(ip string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[ip] = append(a.failures[ip], time.Now())
	return len(a.failures[ip])
}

// Login checks credentials. When TOTP is enabled and code is empty it returns
// ErrNeedTOTP after the password checks out. On success it returns the raw
// session cookie value.
func (a *Auth) Login(username, password, code, ip, ua string) (string, error) {
	if a.locked(ip) {
		return "", ErrLocked
	}
	ad, err := a.Admin()
	if err != nil || ad.Username != username || bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(password)) != nil {
		a.fail(ip)
		return "", ErrBadCredentials
	}
	if ad.TOTPSecret != "" {
		if code == "" {
			return "", ErrNeedTOTP
		}
		if !a.checkSecondFactor(ad, code) {
			a.fail(ip)
			return "", ErrBadTOTP
		}
	}
	raw := secret.RandomToken(32)
	now := time.Now()
	err = a.st.DB.Create(&store.Session{ID: secret.Hash(raw), IP: ip, UA: ua, CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(sessionTTL)}).Error
	return raw, err
}

// Failures returns the number of recent failures for ip (for alerts).
func (a *Auth) Failures(ip string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.failures[ip])
}

func (a *Auth) checkSecondFactor(ad *store.Admin, code string) bool {
	sec, err := a.box.Open(ad.TOTPSecret)
	if err == nil && VerifyTOTP(sec, code) {
		return true
	}
	// Recovery codes are single use.
	var hashes []string
	_ = json.Unmarshal([]byte(ad.RecoveryHash), &hashes)
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	for i, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(code)) == nil {
			hashes = append(hashes[:i], hashes[i+1:]...)
			b, _ := json.Marshal(hashes)
			a.st.DB.Model(ad).Update("recovery_hash", string(b))
			return true
		}
	}
	return false
}

// VerifyTOTPNow re-checks a code for sensitive actions (restore backup, ...).
// When 2FA is off it requires the password instead.
func (a *Auth) VerifySensitive(code string) bool {
	ad, err := a.Admin()
	if err != nil {
		return false
	}
	if ad.TOTPSecret == "" {
		return bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(code)) == nil
	}
	sec, err := a.box.Open(ad.TOTPSecret)
	return err == nil && VerifyTOTP(sec, code)
}

// Check validates a cookie value and refreshes LastSeen.
func (a *Auth) Check(raw, ip string) (*store.Session, bool) {
	if raw == "" {
		return nil, false
	}
	var s store.Session
	if err := a.st.DB.First(&s, "id = ?", secret.Hash(raw)).Error; err != nil {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		a.st.DB.Delete(&s)
		return nil, false
	}
	if time.Since(s.LastSeen) > time.Minute {
		a.st.DB.Model(&s).Updates(map[string]any{"last_seen": time.Now(), "ip": ip})
	}
	return &s, true
}

func (a *Auth) Logout(raw string) {
	a.st.DB.Delete(&store.Session{}, "id = ?", secret.Hash(raw))
}

func (a *Auth) Sessions() []store.Session {
	var out []store.Session
	a.st.DB.Where("expires_at > ?", time.Now()).Order("last_seen desc").Find(&out)
	return out
}

func (a *Auth) Kick(id string) error {
	return a.st.DB.Delete(&store.Session{}, "id = ?", id).Error
}

func (a *Auth) ChangePassword(old, nw string) error {
	ad, err := a.Admin()
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(old)) != nil {
		return ErrBadCredentials
	}
	if len(nw) < 10 {
		return errors.New("新密码至少 10 位")
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(nw), bcrypt.DefaultCost)
	return a.st.DB.Model(ad).Updates(map[string]any{"password_hash": string(hash), "password_at": time.Now()}).Error
}

// BeginTOTP stores a pending secret and returns it with its otpauth URI.
func (a *Auth) BeginTOTP() (string, string, error) {
	ad, err := a.Admin()
	if err != nil {
		return "", "", err
	}
	sec := NewTOTPSecret()
	if err := a.st.DB.Model(ad).Update("totp_pending", a.box.Seal(sec)).Error; err != nil {
		return "", "", err
	}
	return sec, TOTPURI(sec, ad.Username, "VPS Panel"), nil
}

// ConfirmTOTP enables 2FA and returns 8 recovery codes.
func (a *Auth) ConfirmTOTP(code string) ([]string, error) {
	ad, err := a.Admin()
	if err != nil {
		return nil, err
	}
	sec, err := a.box.Open(ad.TOTPPending)
	if err != nil || sec == "" || !VerifyTOTP(sec, code) {
		return nil, ErrBadTOTP
	}
	codes, hashes := newRecoveryCodes()
	b, _ := json.Marshal(hashes)
	err = a.st.DB.Model(ad).Updates(map[string]any{"totp_secret": ad.TOTPPending, "totp_pending": "", "recovery_hash": string(b)}).Error
	return codes, err
}

func (a *Auth) RegenerateRecovery() ([]string, error) {
	ad, err := a.Admin()
	if err != nil {
		return nil, err
	}
	codes, hashes := newRecoveryCodes()
	b, _ := json.Marshal(hashes)
	return codes, a.st.DB.Model(ad).Update("recovery_hash", string(b)).Error
}

func (a *Auth) DisableTOTP(code string) error {
	ad, err := a.Admin()
	if err != nil {
		return err
	}
	if !a.checkSecondFactor(ad, code) {
		return ErrBadTOTP
	}
	return a.st.DB.Model(ad).Updates(map[string]any{"totp_secret": "", "recovery_hash": ""}).Error
}

func (a *Auth) RecoveryLeft() int {
	ad, err := a.Admin()
	if err != nil {
		return 0
	}
	var hashes []string
	_ = json.Unmarshal([]byte(ad.RecoveryHash), &hashes)
	return len(hashes)
}

func newRecoveryCodes() ([]string, []string) {
	var codes, hashes []string
	for i := 0; i < 8; i++ {
		c := strings.ToLower(secret.RandomToken(6))
		c = strings.NewReplacer("-", "x", "_", "y").Replace(c)
		h, _ := bcrypt.GenerateFromPassword([]byte(c), bcrypt.DefaultCost)
		codes = append(codes, c[:4]+"-"+c[4:])
		hashes = append(hashes, string(h))
	}
	return codes, hashes
}

// IPAllowed checks ip against newline separated IPs / CIDRs. Empty = allow.
func IPAllowed(list, ip string) bool {
	list = strings.TrimSpace(list)
	if list == "" {
		return true
	}
	addr := net.ParseIP(ip)
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "/") {
			if _, n, err := net.ParseCIDR(line); err == nil && addr != nil && n.Contains(addr) {
				return true
			}
		} else if line == ip {
			return true
		}
	}
	return false
}

// ResetAdmin sets a new random password, turns off 2FA and ends all
// sessions. Used by `vps-panel reset-admin` on the panel host.
func (a *Auth) ResetAdmin() (string, error) {
	ad, err := a.Admin()
	if err != nil {
		return "", err
	}
	pw := secret.RandomToken(12)
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := a.st.DB.Model(ad).Updates(map[string]any{"password_hash": string(hash), "password_at": time.Now(), "totp_secret": "", "totp_pending": "", "recovery_hash": ""}).Error; err != nil {
		return "", err
	}
	a.st.DB.Where("1 = 1").Delete(&store.Session{})
	return pw, nil
}
