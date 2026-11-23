package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWithBase(t *testing.T) {
	page := []byte("<html><head><meta charset=\"utf-8\"></head></html>")
	for prefix, want := range map[string]string{
		"/":       `<html><head><base href="/"><meta charset="utf-8"></head></html>`,
		"/secret": `<html><head><base href="/secret/"><meta charset="utf-8"></head></html>`,
		`/a"b`:    `<html><head><base href="/a&#34;b/"><meta charset="utf-8"></head></html>`,
	} {
		if got := string(withBase(page, prefix)); got != want {
			t.Errorf("withBase(%q) = %s, want %s", prefix, got, want)
		}
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSPAPrecompressedAssets(t *testing.T) {
	a, _ := newTestAPI(t, false)
	jsRaw := []byte("console.log('vps-panel-precompressed');\n")
	cssRaw := []byte("body{color:#0f7b45}\n")
	a.Web = fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<html><head></head><body>SPA</body></html>")},
		"assets/app-123.js.gz":  &fstest.MapFile{Data: gzipBytes(t, jsRaw)},
		"assets/app-123.css.gz": &fstest.MapFile{Data: gzipBytes(t, cssRaw)},
		"sw.js.gz":              &fstest.MapFile{Data: gzipBytes(t, jsRaw)},
	}
	r := a.Router()

	// 1. Client accepts gzip: serve .gz directly with Content-Encoding: gzip and JS Content-Type.
	req := httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("gzip asset status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type = %q, want javascript", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", cc)
	}
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body not valid gzip: %v", err)
	}
	gotJS, _ := io.ReadAll(gr)
	gr.Close()
	if !bytes.Equal(gotJS, jsRaw) {
		t.Fatalf("decompressed body = %q, want %q", gotJS, jsRaw)
	}

	// 2. Client does not accept gzip: transparently decompress on the fly.
	req2 := httptest.NewRequest(http.MethodGet, "/assets/app-123.css", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("plain fallback status = %d, want 200", w2.Code)
	}
	if got := w2.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("unexpected Content-Encoding = %q", got)
	}
	if ct := w2.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", ct)
	}
	if !bytes.Equal(w2.Body.Bytes(), cssRaw) {
		t.Fatalf("plain body = %q, want %q", w2.Body.Bytes(), cssRaw)
	}

	// 3. sw.js.gz keeps no-cache header.
	req3 := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	req3.Header.Set("Accept-Encoding", "gzip")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK || w3.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("sw.js status=%d cache=%q", w3.Code, w3.Header().Get("Cache-Control"))
	}

	// 4. Unknown path still falls back to index.html.
	req4 := httptest.NewRequest(http.MethodGet, "/servers/1", nil)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK || !strings.Contains(w4.Body.String(), "<body>SPA</body>") {
		t.Fatalf("SPA fallback status=%d body=%q", w4.Code, w4.Body.String())
	}
}
