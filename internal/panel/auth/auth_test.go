package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

func TestFailuresMapEvictsExpiredEntries(t *testing.T) {
	a := New(nil, nil)
	expired := time.Now().Add(-lockFor - time.Minute)

	// 1. locked() deletes the key instead of storing an empty slice.
	a.failures["1.2.3.4"] = []time.Time{expired}
	if a.locked("1.2.3.4") {
		t.Fatal("expired IP should not be locked")
	}
	if _, exists := a.failures["1.2.3.4"]; exists {
		t.Fatal("expired IP key was not deleted from failures map")
	}

	// 2. High-cardinality expired IPs are pruned when new failures arrive.
	for i := 0; i < 300; i++ {
		a.failures[fmt.Sprintf("10.0.%d.%d", i/256, i%256)] = []time.Time{expired}
	}
	a.fail("203.0.113.1")
	if got := len(a.failures); got != 1 {
		t.Fatalf("failures map retained %d stale keys, want 1", got)
	}
}

func TestIPAllowedMultiDelimiters(t *testing.T) {
	if !IPAllowed("", "8.8.8.8") {
		t.Fatal("empty allowlist must allow all")
	}
	list := "1.2.3.4, 5.6.7.8; 10.0.0.0/24\r\n2001:db8::1\n2001:db8:abcd::/48"
	for _, allowed := range []string{"1.2.3.4", "5.6.7.8", "10.0.0.42", "2001:db8::1", "2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8:abcd:1::5"} {
		if !IPAllowed(list, allowed) {
			t.Errorf("IPAllowed(%q) = false, want true", allowed)
		}
	}
	for _, denied := range []string{"1.2.3.5", "10.0.1.1", "2001:db8::2", "192.168.1.1"} {
		if IPAllowed(list, denied) {
			t.Errorf("IPAllowed(%q) = true, want false", denied)
		}
	}
}

func TestFailuresMapCapAndIPv6Slash64(t *testing.T) {
	a := New(nil, nil)
	// IPv6 addresses in the same /64 share one failure bucket.
	for i := 1; i <= maxFailures; i++ {
		a.fail(fmt.Sprintf("2001:db8:1:2::%x", i))
	}
	if !a.locked("2001:db8:1:2:ffff::99") {
		t.Fatal("IPv6 /64 rotation bypassed lockout")
	}
	if a.locked("2001:db8:1:3::1") {
		t.Fatal("different IPv6 /64 should not be locked")
	}

	// Cap at maxFailureKeys.
	for i := 0; i < maxFailureKeys+100; i++ {
		a.fail(fmt.Sprintf("10.%d.%d.%d", (i>>16)&0xff, (i>>8)&0xff, i&0xff))
	}
	a.mu.Lock()
	n := len(a.failures)
	a.mu.Unlock()
	if n > maxFailureKeys {
		t.Fatalf("failures map size %d exceeded cap %d", n, maxFailureKeys)
	}
}

func newTestAuth(t *testing.T) *Auth {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	box, err := secret.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, box)
	if _, err := a.EnsureAdmin("admin", "CorrectPass123!"); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAuthTOTPReplayAndRateLimits(t *testing.T) {
	a := newTestAuth(t)

	// Enable TOTP.
	sec, _, err := a.BeginTOTP()
	if err != nil {
		t.Fatal(err)
	}
	step := uint64(time.Now().Unix() / 30)
	codeNow, err := totpAt(sec, step)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTOTPFor(codeNow, "1.1.1.1"); err != nil {
		t.Fatalf("ConfirmTOTPFor: %v", err)
	}
	// Replaying the same step code must fail!
	if err := a.VerifySensitiveIP(codeNow, "1.1.1.1"); err == nil {
		t.Fatal("replayed TOTP code was accepted")
	}
	// Next step (+1 within ±1 window) succeeds once, then replay fails.
	codeNext, err := totpAt(sec, step+1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.VerifySensitiveIP(codeNext, "1.1.1.1"); err != nil {
		t.Fatalf("fresh step+1 code rejected: %v", err)
	}
	if err := a.VerifySensitiveIP(codeNext, "1.1.1.1"); err == nil {
		t.Fatal("replayed step+1 TOTP code was accepted")
	}

	// Rate-limit lockout on VerifySensitiveIP, ChangePasswordFor, DisableTOTPFor.
	for i := 0; i < maxFailures; i++ {
		_ = a.VerifySensitiveIP("000000", "2.2.2.2")
	}
	if err := a.VerifySensitiveIP("000000", "2.2.2.2"); !errors.Is(err, ErrLocked) {
		t.Fatalf("VerifySensitiveIP lockout = %v, want ErrLocked", err)
	}
	for i := 0; i < maxFailures; i++ {
		_ = a.ChangePasswordFor("wrong", "NewPassword123!", "3.3.3.3")
	}
	if err := a.ChangePasswordFor("CorrectPass123!", "NewPassword123!", "3.3.3.3"); !errors.Is(err, ErrLocked) {
		t.Fatalf("ChangePasswordFor lockout = %v, want ErrLocked", err)
	}
	for i := 0; i < maxFailures; i++ {
		_ = a.DisableTOTPFor("000000", "4.4.4.4")
	}
	if err := a.DisableTOTPFor("000000", "4.4.4.4"); !errors.Is(err, ErrLocked) {
		t.Fatalf("DisableTOTPFor lockout = %v, want ErrLocked", err)
	}
	// Unknown username still returns ErrBadCredentials and increments failures.
	if _, err := a.Login("nonexistent", "whatever", "", "5.5.5.5", "ua"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user login = %v, want ErrBadCredentials", err)
	}
	if a.Failures("5.5.5.5") != 1 {
		t.Fatalf("unknown user failures = %d, want 1", a.Failures("5.5.5.5"))
	}
}

func TestTOTPNulledColumnMigrationDoesNotLockOut(t *testing.T) {
	a := newTestAuth(t)
	sec, _, err := a.BeginTOTP()
	if err != nil {
		t.Fatal(err)
	}
	step := uint64(time.Now().Unix() / 30)
	code, err := totpAt(sec, step)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTOTPFor(code, "9.9.9.1"); err != nil {
		t.Fatal(err)
	}

	// Simulate a legacy DB table where last_totp_step was added as nullable NULL.
	db := a.st.DB
	if err := db.Exec(`CREATE TABLE admins_legacy (
		id integer primary key autoincrement,
		username text,
		password_hash text,
		password_at datetime,
		totp_secret text,
		totp_pending text,
		recovery_hash text,
		last_totp_step integer
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO admins_legacy (id, username, password_hash, password_at, totp_secret, totp_pending, recovery_hash, last_totp_step)
		SELECT id, username, password_hash, password_at, totp_secret, totp_pending, recovery_hash, NULL FROM admins`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DROP TABLE admins`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE admins_legacy RENAME TO admins`).Error; err != nil {
		t.Fatal(err)
	}

	// Confirm the row genuinely holds SQL NULL in last_totp_step.
	var nullCount int64
	db.Raw(`SELECT COUNT(*) FROM admins WHERE last_totp_step IS NULL`).Scan(&nullCount)
	if nullCount != 1 {
		t.Fatalf("expected 1 admin row with NULL last_totp_step, got %d", nullCount)
	}

	// First verification with SQL NULL must succeed (no lockout), then replay must fail.
	if err := a.VerifySensitiveIP(code, "9.9.9.2"); err != nil {
		t.Fatalf("TOTP verification failed on NULL last_totp_step row: %v", err)
	}
	if err := a.VerifySensitiveIP(code, "9.9.9.2"); err == nil {
		t.Fatal("replayed TOTP code after NULL migration was accepted")
	}
}
