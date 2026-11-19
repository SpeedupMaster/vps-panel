package tasks

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
