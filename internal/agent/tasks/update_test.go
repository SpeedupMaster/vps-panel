package tasks

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpspanel/internal/agent/config"
)

func TestDownloadToFileAndVerifySig(t *testing.T) {
	payload := []byte("agent-binary-content")
	sum := sha256.Sum256(payload)
	wantSum := hex.EncodeToString(sum[:])

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "vpsagent.new")
	gotSum, err := downloadToFile(context.Background(), srv.URL, dst, 1<<20)
	if err != nil {
		t.Fatalf("downloadToFile failed: %v", err)
	}
	if gotSum != wantSum {
		t.Fatalf("sha256 = %s, want %s", gotSum, wantSum)
	}
	if err := verifyFileSig(pub, dst, sigB64); err != nil {
		t.Fatalf("verifyFileSig failed: %v", err)
	}
	if err := verifyFileSig(pub, dst, base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))); err == nil {
		t.Fatal("expected invalid signature to fail")
	}

	tooBig := filepath.Join(t.TempDir(), "too-big")
	if _, err := downloadToFile(context.Background(), srv.URL, tooBig, 5); err == nil {
		t.Fatal("expected size limit error")
	}
	if _, err := os.Stat(tooBig); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be removed on size limit error, stat err=%v", err)
	}
}

func TestParseAndLoadSignKeyBothFormats(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rawB64 := base64.StdEncoding.EncodeToString(pub)
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	spkiB64 := base64.StdEncoding.EncodeToString(spki)

	payload := []byte("signed-agent-binary")
	binPath := filepath.Join(t.TempDir(), "vpsagent")
	if err := os.WriteFile(binPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))

	oldDir := config.Dir
	defer func() { config.Dir = oldDir }()
	config.Dir = t.TempDir()

	for name, encoded := range map[string]string{"raw32": rawB64, "spki44": spkiB64} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(config.Dir, "sign.pub"), []byte(encoded+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			pk, err := loadSignKey()
			if err != nil {
				t.Fatalf("loadSignKey(%s): %v", name, err)
			}
			if !pk.Equal(pub) {
				t.Fatalf("loaded key mismatch for %s", name)
			}
			if err := verifyFileSig(pk, binPath, sigB64); err != nil {
				t.Fatalf("verifyFileSig(%s): %v", name, err)
			}
		})
	}

	if _, err := parseSignKey("not-valid-base64!!!"); err == nil {
		t.Fatal("expected invalid base64 to fail")
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseSignKey(base64.StdEncoding.EncodeToString(ecDER)); err == nil {
		t.Fatal("expected non-ed25519 SPKI key to fail")
	}
}

func TestRollbackScriptUsesAtomicRenameAnd60sSleep(t *testing.T) {
	p := pendingUpdate{
		Binary:  "/usr/local/bin/vpsagent",
		Backup:  "/usr/local/bin/vpsagent.old",
		Version: "1.2.3",
		At:      1700000000,
	}
	script := rollbackScript(p, "/var/lib/vpsagent/update-pending.json")
	for _, want := range []string{
		"trap '' HUP;",
		"sleep 60;",
		"cp -f '/usr/local/bin/vpsagent.old' '/usr/local/bin/vpsagent.rollback'",
		"mv -f '/usr/local/bin/vpsagent.rollback' '/usr/local/bin/vpsagent'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("rollbackScript missing %q in:\n%s", want, script)
		}
	}
	if strings.Contains(script, "cp -f '/usr/local/bin/vpsagent.old' '/usr/local/bin/vpsagent'") {
		t.Fatalf("rollbackScript must not cp directly onto running binary:\n%s", script)
	}
}
