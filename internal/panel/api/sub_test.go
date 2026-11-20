package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/hub"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func newTestAPI(t *testing.T, withWeb bool) (*API, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db, err := st.DB.DB(); err == nil {
			db.Close()
		}
	})
	box, err := secret.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	au := auth.New(st, box)
	if _, err := au.EnsureAdmin("admin", "secretpassword123"); err != nil {
		t.Fatal(err)
	}
	sid, err := au.Login("admin", "secretpassword123", "", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	p := service.New(st, box, au, hub.New(st, box), dir)
	sv, _, err := p.CreateServer(service.ServerInput{Name: "HK-1", Mode: "node", CapReality: true, CountryCode: "HK"})
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Model(sv).Updates(map[string]any{"ipv4": "1.2.3.4"})
	if _, err := p.CreateRoute(service.RouteInput{Type: "direct", EntryServerID: sv.ID, Name: "hk"}); err != nil {
		t.Fatal(err)
	}
	a := &API{P: p, Script: []byte("#!/bin/sh\necho hi\n")}
	if withWeb {
		a.Web = fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>SPA</body></html>")},
		}
	}
	return a, sid
}

func doReq(h http.Handler, method, target, host, cookie string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	if host != "" {
		req.Host = host
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		if method != http.MethodGet {
			req.Header.Set("X-Requested-With", "vps-panel")
			req.Header.Set("Content-Type", "application/json")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestServeSubRootAndLegacyPaths(t *testing.T) {
	a, _ := newTestAPI(t, true)
	if _, err := a.P.CreateSubToken("手机", "", "my-phone", ""); err != nil {
		t.Fatal(err)
	}
	disabled, err := a.P.CreateSubToken("旧设备", "", "disabled-sub", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.P.UpdateSubToken(disabled.ID, "旧设备", "", false, nil, nil); err != nil {
		t.Fatal(err)
	}
	r := a.Router()

	// Root custom path GET
	w := doReq(r, http.MethodGet, "/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /my-phone = %d %s", w.Code, w.Body.String())
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	if err != nil || !strings.HasPrefix(string(dec), "vless://") {
		t.Fatalf("GET /my-phone body = %q (err=%v)", w.Body.String(), err)
	}

	// Root custom path HEAD
	w = doReq(r, http.MethodHead, "/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") == "" || w.Body.Len() != 0 {
		t.Fatalf("HEAD /my-phone = %d hdr=%v len=%d", w.Code, w.Header(), w.Body.Len())
	}

	// Legacy /s/:token GET & HEAD still work
	w = doReq(r, http.MethodGet, "/s/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /s/my-phone = %d", w.Code)
	}
	w = doReq(r, http.MethodHead, "/s/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("HEAD /s/my-phone = %d len=%d", w.Code, w.Body.Len())
	}

	// Disabled token at root returns 404 (must not fall through to SPA HTML)
	w = doReq(r, http.MethodGet, "/disabled-sub", "panel.example.com", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /disabled-sub = %d %s, want 404", w.Code, w.Body.String())
	}

	// SPA routes and healthz keep working
	w = doReq(r, http.MethodGet, "/login", "panel.example.com", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SPA") {
		t.Fatalf("GET /login = %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, http.MethodGet, "/healthz", "panel.example.com", "", nil)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("GET /healthz = %d %s", w.Code, w.Body.String())
	}

	// With admin_path set, admin_path still redirects and root subscription still serves
	_ = a.P.St.Set("admin_path", "/secret")
	w = doReq(r, http.MethodGet, "/secret", "panel.example.com", "", nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/secret/" {
		t.Fatalf("GET /secret = %d loc=%q", w.Code, w.Header().Get("Location"))
	}
	w = doReq(r, http.MethodGet, "/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /my-phone with admin_path = %d", w.Code)
	}

	// Without embedded Web FS (dev/API-only mode), root subscription still works
	aNoWeb, _ := newTestAPI(t, false)
	if _, err := aNoWeb.P.CreateSubToken("dev", "", "dev-sub", ""); err != nil {
		t.Fatal(err)
	}
	w = doReq(aNoWeb.Router(), http.MethodGet, "/dev-sub?target=base64", "localhost:8080", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /dev-sub (Web==nil) = %d %s", w.Code, w.Body.String())
	}
}

func TestSubHostIsolation(t *testing.T) {
	a, _ := newTestAPI(t, true)
	_ = a.P.St.Set("panel_url", "https://panel.example.com")
	_ = a.P.St.Set("sub_url", "https://sub.example.com")
	_ = a.P.St.Set("status_enabled", "1")
	if _, err := a.P.CreateSubToken("手机", "", "my-phone", ""); err != nil {
		t.Fatal(err)
	}
	r := a.Router()

	// Subscription domain serves root and legacy /s/ subscription endpoints.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/my-phone?target=base64"},
		{http.MethodHead, "/my-phone?target=base64"},
		{http.MethodGet, "/s/my-phone?target=base64"},
		{http.MethodHead, "/s/my-phone?target=base64"},
	} {
		w := doReq(r, tc.method, tc.path, "sub.example.com", "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s on sub host = %d, want 200", tc.method, tc.path, w.Code)
		}
	}

	// Everything else on the dedicated subscription domain returns 404.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/login"},
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/status"},
		{http.MethodGet, "/install.sh"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/unknown-token"},
		{http.MethodGet, "/s/unknown-token"},
	} {
		w := doReq(r, tc.method, tc.path, "sub.example.com", "", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s on sub host = %d %q, want 404", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// Panel domain still serves SPA, healthz, and subscriptions.
	w := doReq(r, http.MethodGet, "/", "panel.example.com", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SPA") {
		t.Fatalf("GET / on panel host = %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, http.MethodGet, "/my-phone?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /my-phone on panel host = %d", w.Code)
	}
}

func TestSubTokenAPICustomSuffixAndAdminPathConflict(t *testing.T) {
	a, sid := newTestAPI(t, true)
	_ = a.P.St.Set("panel_url", "https://panel.example.com")
	_ = a.P.St.Set("sub_url", "https://sub.example.com")
	r := a.Router()

	// Create with custom suffix
	w := doReq(r, http.MethodPost, "/api/sub/tokens", "panel.example.com", sid, map[string]any{
		"name": "iPad", "target": "mihomo", "token": "my-ipad",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sub/tokens = %d %s", w.Code, w.Body.String())
	}

	// List returns token + root URL without /s/
	w = doReq(r, http.MethodGet, "/api/sub/tokens", "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/sub/tokens = %d", w.Code)
	}
	var list struct {
		Base   string `json:"base"`
		Tokens []struct {
			ID    uint   `json:"id"`
			Name  string `json:"name"`
			Token string `json:"token"`
			URL   string `json:"url"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Base != "https://sub.example.com" || len(list.Tokens) != 1 || list.Tokens[0].Token != "my-ipad" || list.Tokens[0].URL != "https://sub.example.com/my-ipad" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Edit suffix via PUT
	id := list.Tokens[0].ID
	w = doReq(r, http.MethodPut, "/api/sub/tokens/1", "panel.example.com", sid, map[string]any{
		"name": "iPad Pro", "target_default": "singbox", "enabled": true, "token": "ipad-pro",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/sub/tokens/%d = %d %s", id, w.Code, w.Body.String())
	}
	if w := doReq(r, http.MethodGet, "/my-ipad?target=base64", "sub.example.com", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("old token still served: %d", w.Code)
	}
	if w := doReq(r, http.MethodGet, "/ipad-pro?target=base64", "sub.example.com", "", nil); w.Code != http.StatusOK {
		t.Fatalf("new token failed: %d", w.Code)
	}

	// Inline update without token field preserves the existing suffix.
	w = doReq(r, http.MethodPut, "/api/sub/tokens/1", "panel.example.com", sid, map[string]any{
		"name": "iPad Pro", "target_default": "mihomo", "enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/sub/tokens/%d (no token) = %d %s", id, w.Code, w.Body.String())
	}
	if w := doReq(r, http.MethodGet, "/ipad-pro?target=base64", "sub.example.com", "", nil); w.Code != http.StatusOK {
		t.Fatalf("token lost after inline update: %d", w.Code)
	}

	// Setting admin_path equal to an existing token fails with 400
	w = doReq(r, http.MethodPut, "/api/settings", "panel.example.com", sid, map[string]any{
		"admin_path": "/ipad-pro",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings conflicting admin_path = %d %s, want 400", w.Code, w.Body.String())
	}
}

func TestSubTokenPerLinkRoutesAPI(t *testing.T) {
	a, sid := newTestAPI(t, true)
	sv2, _, err := a.P.CreateServer(service.ServerInput{Name: "SG-1", Mode: "node", CapReality: true, CountryCode: "SG"})
	if err != nil {
		t.Fatal(err)
	}
	a.P.St.DB.Model(sv2).Updates(map[string]any{"ipv4": "5.6.7.8"})
	r2, err := a.P.CreateRoute(service.RouteInput{Type: "direct", EntryServerID: sv2.ID, Name: "sg"})
	if err != nil {
		t.Fatal(err)
	}
	var r1 store.Route
	if err := a.P.St.DB.Where("id <> ?", r2.ID).First(&r1).Error; err != nil {
		t.Fatal(err)
	}
	r := a.Router()

	r1ID := strconv.FormatUint(uint64(r1.ID), 10)
	r2ID := strconv.FormatUint(uint64(r2.ID), 10)

	// Create token scoped to r1 only.
	w := doReq(r, http.MethodPost, "/api/sub/tokens", "panel.example.com", sid, map[string]any{
		"name": "仅香港", "target": "", "token": "hk-only", "routes": r1ID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sub/tokens = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID     uint   `json:"id"`
		Routes string `json:"routes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Routes != r1ID {
		t.Fatalf("created.Routes = %q, want %q", created.Routes, r1ID)
	}

	// Serving /hk-only returns only r1.
	w = doReq(r, http.MethodGet, "/hk-only?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /hk-only = %d %s", w.Code, w.Body.String())
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dec), r1.UUID) || strings.Contains(string(dec), r2.UUID) {
		t.Fatalf("GET /hk-only decoded = %q, want only r1 (%s)", string(dec), r1.UUID)
	}

	// Inline PUT without routes preserves existing routes.
	tokPath := "/api/sub/tokens/" + strconv.FormatUint(uint64(created.ID), 10)
	w = doReq(r, http.MethodPut, tokPath, "panel.example.com", sid, map[string]any{
		"name": "仅香港", "target_default": "mihomo", "enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %s (no routes) = %d %s", tokPath, w.Code, w.Body.String())
	}

	// Preview with token_id returns 1 node (r1), while global preview returns 2 nodes.
	w = doReq(r, http.MethodGet, "/api/sub/preview?target=base64&token_id="+strconv.FormatUint(uint64(created.ID), 10), "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/sub/preview?token_id = %d %s", w.Code, w.Body.String())
	}
	var prev struct {
		Content string `json:"content"`
		Nodes   int    `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &prev); err != nil {
		t.Fatal(err)
	}
	if prev.Nodes != 1 || !strings.Contains(prev.Content, r1.UUID) || strings.Contains(prev.Content, r2.UUID) {
		t.Fatalf("token preview = %+v", prev)
	}

	w = doReq(r, http.MethodGet, "/api/sub/preview?target=base64", "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/sub/preview = %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &prev); err != nil {
		t.Fatal(err)
	}
	if prev.Nodes != 2 {
		t.Fatalf("global preview nodes = %d, want 2", prev.Nodes)
	}

	// Updating routes to r2 switches the token's subscription.
	w = doReq(r, http.MethodPut, tokPath, "panel.example.com", sid, map[string]any{
		"name": "仅新加坡", "target_default": "", "enabled": true, "routes": r2ID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %s (r2) = %d %s", tokPath, w.Code, w.Body.String())
	}
	w = doReq(r, http.MethodGet, "/hk-only?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /hk-only after switch = %d", w.Code)
	}
	dec, err = base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dec), r2.UUID) || strings.Contains(string(dec), r1.UUID) {
		t.Fatalf("after switch decoded = %q, want only r2 (%s)", string(dec), r2.UUID)
	}

	// Clearing routes to "" restores global default (both r1 and r2).
	w = doReq(r, http.MethodPut, tokPath, "panel.example.com", sid, map[string]any{
		"name": "全部", "target_default": "", "enabled": true, "routes": "",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %s (clear) = %d %s", tokPath, w.Code, w.Body.String())
	}
	w = doReq(r, http.MethodGet, "/hk-only?target=base64", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /hk-only after clear = %d", w.Code)
	}
	dec, err = base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dec), r1.UUID) || !strings.Contains(string(dec), r2.UUID) {
		t.Fatalf("after clear decoded = %q, want both r1 and r2", string(dec))
	}

	// Custom Chinese profile name propagates to both Profile-Title and Content-Disposition.
	w = doReq(r, http.MethodPut, "/api/settings", "panel.example.com", sid, map[string]any{
		"sub_profile_name": "加速师",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings sub_profile_name = %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, http.MethodHead, "/hk-only?target=mihomo", "panel.example.com", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("HEAD /hk-only = %d", w.Code)
	}
	wantTitle := "base64:" + base64.StdEncoding.EncodeToString([]byte("加速师"))
	if got := w.Header().Get("Profile-Title"); got != wantTitle {
		t.Fatalf("Profile-Title = %q, want %q", got, wantTitle)
	}
	wantCD := `attachment; filename="%E5%8A%A0%E9%80%9F%E5%B8%88"; filename*=UTF-8''%E5%8A%A0%E9%80%9F%E5%B8%88`
	if got := w.Header().Get("Content-Disposition"); got != wantCD {
		t.Fatalf("Content-Disposition = %q, want %q", got, wantCD)
	}
}
