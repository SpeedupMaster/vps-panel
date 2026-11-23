package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpspanel/internal/common/version"
)

func TestDownloadGzipBundledAgent(t *testing.T) {
	a, _ := newTestAPI(t, false)
	old := version.Version
	version.Version = "3.0.0"
	defer func() { version.Version = old }()

	raw := append([]byte("\x7fELF"), bytes.Repeat([]byte("A"), 1<<20)...)
	sum := sha256.Sum256(raw)
	wantHash := hex.EncodeToString(sum[:])

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "vpsagent-linux-amd64.gz"), gzipBytes(t, raw), 0o644); err != nil {
		t.Fatal(err)
	}
	a.P.RegisterBundledAgent(src)
	r := a.Router()

	// 1. Plain client (e.g. curl -fsSL in install.sh) gets uncompressed raw ELF.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dl/agent/linux-amd64", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("plain /dl/agent status = %d", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("plain request got Content-Encoding=%q", ce)
	}
	if !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatalf("plain body len=%d, want %d", w.Body.Len(), len(raw))
	}

	// 2. Gzip-capable client gets Content-Encoding: gzip with matching decompressed ELF.
	reqGz := httptest.NewRequest(http.MethodGet, "/dl/agent/linux-amd64", nil)
	reqGz.Header.Set("Accept-Encoding", "gzip")
	wGz := httptest.NewRecorder()
	r.ServeHTTP(wGz, reqGz)
	if wGz.Code != http.StatusOK || wGz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip /dl/agent status=%d ce=%q", wGz.Code, wGz.Header().Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(wGz.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(gr)
	gr.Close()
	if !bytes.Equal(got, raw) {
		t.Fatal("decompressed /dl/agent body mismatch")
	}

	// 3. .sha256 returns checksum of the UNCOMPRESSED ELF.
	wSha := httptest.NewRecorder()
	r.ServeHTTP(wSha, httptest.NewRequest(http.MethodGet, "/dl/agent/linux-amd64.sha256", nil))
	if wSha.Code != http.StatusOK || !strings.HasPrefix(wSha.Body.String(), wantHash+"  vpsagent") {
		t.Fatalf("sha256 status=%d body=%q want=%s", wSha.Code, wSha.Body.String(), wantHash)
	}

	// 4. Versioned release endpoint /dl/agentv/<ver>/... also serves uncompressed or gzipped cleanly.
	wVer := httptest.NewRecorder()
	r.ServeHTTP(wVer, httptest.NewRequest(http.MethodGet, "/dl/agentv/3.0.0/vpsagent-linux-amd64", nil))
	if wVer.Code != http.StatusOK || !bytes.Equal(wVer.Body.Bytes(), raw) {
		t.Fatalf("/dl/agentv plain status=%d len=%d", wVer.Code, wVer.Body.Len())
	}
	reqVerGz := httptest.NewRequest(http.MethodGet, "/dl/agentv/3.0.0/vpsagent-linux-amd64", nil)
	reqVerGz.Header.Set("Accept-Encoding", "gzip")
	wVerGz := httptest.NewRecorder()
	r.ServeHTTP(wVerGz, reqVerGz)
	if wVerGz.Code != http.StatusOK || wVerGz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("/dl/agentv gzip status=%d ce=%q", wVerGz.Code, wVerGz.Header().Get("Content-Encoding"))
	}
}
