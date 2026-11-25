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
	CookieName     = "vp_session"
	sessionTTL     = 7 * 24 * time.Hour
	maxFailures    = 5
	lockFor        = 15 * time.Minute
	maxFailureKeys = 4096
)

// dummyBcryptHash is compared when the username does not match to prevent
// timing-based username enumeration.
var dummyBcryptHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

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

func normalizeRateKey(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func (a *Auth) locked(ip string) bool {
	key := normalizeRateKey(ip)
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().Add(-lockFor)
	var keep []time.Time
	for _, t := range a.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(a.failures, key)
		return false
	}
	a.failures[key] = keep
	return len(keep) >= maxFailures
}

func (a *Auth) fail(ip string) int {
	key := normalizeRateKey(ip)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	cut := now.Add(-lockFor)
	if len(a.failures) > 256 {
		for k, ts := range a.failures {
			if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
				delete(a.failures, k)
			}
		}
	}
	if _, exists := a.failures[key]; !exists && len(a.failures) >= maxFailureKeys {
		// Evict expired first, then the oldest-seen key to bound memory.
		for k, ts := range a.failures {
			if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
				delete(a.failures, k)
			}
		}
		for len(a.failures) >= maxFailureKeys {
			var oldestKey string
			var oldest time.Time
			for k, ts := range a.failures {
				var last time.Time
				if len(ts) > 0 {
					last = ts[len(ts)-1]
				}
				if oldestKey == "" || last.Before(oldest) {
					oldestKey, oldest = k, last
				}
			}
			if oldestKey == "" {
				break
			}
			delete(a.failures, oldestKey)
		}
	}
	var keep []time.Time
	for _, t := range a.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	a.failures[key] = keep
	return len(keep)
}

// Login checks credentials. When TOTP is enabled and code is empty it returns
// ErrNeedTOTP after the password checks out. On success it returns the raw
// session cookie value.
func (a *Auth) Login(username, password, code, ip, ua string) (string, error) {
	if a.locked(ip) {
		return "", ErrLocked
	}
	ad, err := a.Admin()
	if err != nil || ad.Username != username {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		a.fail(ip)
		return "", ErrBadCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(password)) != nil {
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
	key := normalizeRateKey(ip)
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.failures[key])
}

func (a *Auth) verifyAndConsumeTOTP(ad *store.Admin, sec, code string) bool {
	step, ok := VerifyTOTPStep(sec, code)
	if !ok {
		return false
	}
	res := a.st.DB.Model(&store.Admin{}).
		Where("id = ? AND last_totp_step < ?", ad.ID, step).
		Update("last_totp_step", step)
	return res.Error == nil && res.RowsAffected == 1
}

func (a *Auth) checkSecondFactor(ad *store.Admin, code string) bool {
	sec, err := a.box.Open(ad.TOTPSecret)
	if err == nil && a.verifyAndConsumeTOTP(ad, sec, code) {
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

func rateKeyFor(scope, ip string) string {
	if norm := normalizeRateKey(ip); norm != "" {
		return scope + ":" + norm
	}
	return scope
}

// VerifySensitiveIP re-checks a TOTP code (or password when 2FA is off) for
// sensitive actions with failure rate-limiting/lockout.
func (a *Auth) VerifySensitiveIP(code, ip string) error {
	rk := rateKeyFor("sensitive", ip)
	if a.locked(rk) {
		return ErrLocked
	}
	ad, err := a.Admin()
	if err != nil {
		a.fail(rk)
		return ErrBadCredentials
	}
	if ad.TOTPSecret == "" {
		if bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(code)) != nil {
			a.fail(rk)
			return ErrBadCredentials
		}
		return nil
	}
	sec, err := a.box.Open(ad.TOTPSecret)
	if err != nil || !a.verifyAndConsumeTOTP(ad, sec, code) {
		a.fail(rk)
		return ErrBadTOTP
	}
	return nil
}

// VerifySensitive re-checks a code for sensitive actions (restore backup, ...).
// When 2FA is off it requires the password instead.
func (a *Auth) VerifySensitive(code string) bool {
	return a.VerifySensitiveIP(code, "") == nil
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

// KickOthers ends every session except keep (used after a password change).
func (a *Auth) KickOthers(keep string) error {
	return a.st.DB.Delete(&store.Session{}, "id <> ?", keep).Error
}

func (a *Auth) ChangePassword(old, nw string) error {
	return a.ChangePasswordFor(old, nw, "")
}

func (a *Auth) ChangePasswordFor(old, nw, ip string) error {
	rk := rateKeyFor("password", ip)
	if a.locked(rk) {
		return ErrLocked
	}
	ad, err := a.Admin()
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(ad.PasswordHash), []byte(old)) != nil {
		a.fail(rk)
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
	return a.ConfirmTOTPFor(code, "")
}

func (a *Auth) ConfirmTOTPFor(code, ip string) ([]string, error) {
	rk := rateKeyFor("totp_confirm", ip)
	if a.locked(rk) {
		return nil, ErrLocked
	}
	ad, err := a.Admin()
	if err != nil {
		return nil, err
	}
	sec, err := a.box.Open(ad.TOTPPending)
	if err != nil || sec == "" {
		a.fail(rk)
		return nil, ErrBadTOTP
	}
	step, ok := VerifyTOTPStep(sec, code)
	if !ok {
		a.fail(rk)
		return nil, ErrBadTOTP
	}
	codes, hashes := newRecoveryCodes()
	b, _ := json.Marshal(hashes)
	err = a.st.DB.Model(ad).Updates(map[string]any{
		"totp_secret":    ad.TOTPPending,
		"totp_pending":   "",
		"recovery_hash":  string(b),
		"last_totp_step": step,
	}).Error
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
	return a.DisableTOTPFor(code, "")
}

func (a *Auth) DisableTOTPFor(code, ip string) error {
	rk := rateKeyFor("totp_disable", ip)
	if a.locked(rk) {
		return ErrLocked
	}
	ad, err := a.Admin()
	if err != nil {
		return err
	}
	if !a.checkSecondFactor(ad, code) {
		a.fail(rk)
		return ErrBadTOTP
	}
	return a.st.DB.Model(ad).Updates(map[string]any{"totp_secret": "", "recovery_hash": "", "last_totp_step": int64(0)}).Error
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

// IPAllowed checks ip against newline, comma, or semicolon separated IPs / CIDRs. Empty = allow.
func IPAllowed(list, ip string) bool {
	list = strings.TrimSpace(list)
	if list == "" {
		return true
	}
	addr := net.ParseIP(strings.TrimSpace(ip))
	items := strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	for _, line := range items {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "/") {
			if _, n, err := net.ParseCIDR(line); err == nil && addr != nil && n.Contains(addr) {
				return true
			}
		} else if parsed := net.ParseIP(line); parsed != nil && addr != nil && parsed.Equal(addr) {
			return true
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
	if err := a.st.DB.Model(ad).Updates(map[string]any{"password_hash": string(hash), "password_at": time.Now(), "totp_secret": "", "totp_pending": "", "recovery_hash": "", "last_totp_step": int64(0)}).Error; err != nil {
		return "", err
	}
	a.st.DB.Where("1 = 1").Delete(&store.Session{})
	return pw, nil
}
