package secret

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreateAndReloadMasterKey(t *testing.T) {
	dir := t.TempDir()
	box1, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(create): %v", err)
	}
	keyPath := filepath.Join(dir, "master.key")
	rawKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("master.key missing: %v", err)
	}
	if len(rawKey) != 64 {
		t.Fatalf("master.key len = %d, want 64 hex chars", len(rawKey))
	}

	ct := box1.Seal("top-secret-value")
	if ct == "" || ct == "top-secret-value" {
		t.Fatalf("Seal returned unexpected value: %q", ct)
	}

	// Reload from disk and verify decrypt matches.
	box2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(reload): %v", err)
	}
	got, err := box2.Open(ct)
	if err != nil || got != "top-secret-value" {
		t.Fatalf("Open after reload = (%q, %v), want (top-secret-value, nil)", got, err)
	}
	if box2.MustOpen(ct) != "top-secret-value" {
		t.Fatalf("MustOpen failed")
	}

	// Corrupted master.key must fail to load.
	if err := os.WriteFile(keyPath, []byte("not-hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted invalid master.key")
	}
}

func TestSealOpenEdgeCasesAndTamper(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("New accepted invalid AES key size")
	}
	box, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Empty plaintext/ciphertext stays empty.
	if got := box.Seal(""); got != "" {
		t.Fatalf("Seal(\"\") = %q, want empty", got)
	}
	if got, err := box.Open(""); err != nil || got != "" {
		t.Fatalf("Open(\"\") = (%q, %v), want (\"\", nil)", got, err)
	}

	// Same plaintext encrypts with fresh random nonces.
	c1 := box.Seal("hello")
	c2 := box.Seal("hello")
	if c1 == c2 {
		t.Fatal("Seal reused nonce for identical plaintexts")
	}

	// Tampered ciphertext must fail authentication.
	raw, err := base64.StdEncoding.DecodeString(c1)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := box.Open(tampered); err == nil {
		t.Fatal("Open accepted tampered ciphertext")
	}
	if box.MustOpen(tampered) != "" {
		t.Fatal("MustOpen returned non-empty on tampered ciphertext")
	}

	// Invalid base64 & short ciphertext fail cleanly.
	if _, err := box.Open("!!!not-base64!!!"); err == nil {
		t.Fatal("Open accepted invalid base64")
	}
	short := base64.StdEncoding.EncodeToString([]byte("abc"))
	if _, err := box.Open(short); err == nil {
		t.Fatal("Open accepted short ciphertext")
	}
}

func TestHashAndRandomToken(t *testing.T) {
	h1 := Hash("token-a")
	h2 := Hash("token-a")
	h3 := Hash("token-b")
	if len(h1) != 64 || h1 != h2 || h1 == h3 {
		t.Fatalf("unexpected Hash outputs: %q %q %q", h1, h2, h3)
	}
	tok1 := RandomToken(24)
	tok2 := RandomToken(24)
	if len(tok1) != 32 || tok1 == tok2 {
		t.Fatalf("unexpected RandomToken outputs: %q %q", tok1, tok2)
	}
}
