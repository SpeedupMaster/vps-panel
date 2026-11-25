package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func doReqConfirm(h http.Handler, method, target, cookie, confirm string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Host = "panel.example.com"
	req.RemoteAddr = "198.51.100.10:12345"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		if method != http.MethodGet {
			req.Header.Set("X-Requested-With", "vps-panel")
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if confirm != "" {
		req.Header.Set("X-Confirm", confirm)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSensitiveEndpointsRequireConfirm(t *testing.T) {
	a, sid := newTestAPI(t, false)
	r := a.Router()

	// Create an extra probe server that can be deleted.
	sv, _, err := a.P.CreateServer(service.ServerInput{Name: "DelTarget", Mode: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	delPath := "/api/servers/" + strconv.FormatUint(uint64(sv.ID), 10)

	// 1. DELETE /api/servers/:id without X-Confirm -> 403.
	if w := doReqConfirm(r, http.MethodDelete, delPath, sid, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("DELETE server without confirm = %d, want 403", w.Code)
	}
	// With valid password -> 200.
	if w := doReqConfirm(r, http.MethodDelete, delPath, sid, "secretpassword123", nil); w.Code != http.StatusOK {
		t.Fatalf("DELETE server with confirm = %d %s, want 200", w.Code, w.Body.String())
	}

	// 2. GET /api/backups/:name without X-Confirm -> 403; with valid password -> 200.
	bkName, err := a.P.BackupNow("manual")
	if err != nil {
		t.Fatal(err)
	}
	if w := doReqConfirm(r, http.MethodGet, "/api/backups/"+bkName, sid, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("GET backup without confirm = %d, want 403", w.Code)
	}
	if w := doReqConfirm(r, http.MethodGet, "/api/backups/"+bkName, sid, "secretpassword123", nil); w.Code != http.StatusOK {
		t.Fatalf("GET backup with confirm = %d, want 200", w.Code)
	}

	// 3. POST /api/servers/:id/ssh-install without X-Confirm -> 403.
	if w := doReqConfirm(r, http.MethodPost, "/api/servers/1/ssh-install", sid, "", map[string]any{"host": "1.2.3.4"}); w.Code != http.StatusForbidden {
		t.Fatalf("POST ssh-install without confirm = %d, want 403", w.Code)
	}
}

func TestSettingsValidationAdminPathCaseInsensitiveAndCSVAndAckInfo(t *testing.T) {
	a, sid := newTestAPI(t, false)
	r := a.Router()

	// Invalid panel_url / sub_url / panel_urls_backup rejected.
	for _, payload := range []map[string]any{
		{"panel_url": "ftp://panel.example.com"},
		{"panel_url": "https://panel.example.com;rm -rf /"},
		{"panel_url": "https://panel.example.com'"},
		{"sub_url": "https://sub.example.com $(whoami)"},
		{"panel_urls_backup": "https://ok.example.com\njavascript:alert(1)"},
	} {
		w := doReq(r, http.MethodPut, "/api/settings", "panel.example.com", sid, payload)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT /api/settings %v = %d, want 400", payload, w.Code)
		}
	}
	// Valid URLs accepted.
	if w := doReq(r, http.MethodPut, "/api/settings", "panel.example.com", sid, map[string]any{
		"panel_url":         "https://panel.example.com",
		"sub_url":           "https://sub.example.com:8443",
		"panel_urls_backup": "https://b1.example.com\nhttps://b2.example.com",
	}); w.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings valid URLs = %d %s", w.Code, w.Body.String())
	}

	// Case-insensitive admin_path vs mixed-case SubToken collision.
	if _, err := a.P.CreateSubToken("Mixed", "", "MixedCaseTok", ""); err != nil {
		t.Fatal(err)
	}
	if w := doReq(r, http.MethodPut, "/api/settings", "panel.example.com", sid, map[string]any{
		"admin_path": "/mixedcasetok",
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("case-insensitive admin_path collision = %d, want 400", w.Code)
	}

	// validTarget rejects loopback/private/link-local/localhost.
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "localhost", "a.localhost"} {
		if err := validTarget(&store.ProbeTarget{Name: "x", Host: host, Type: "tcp", Port: 80}); err == nil {
			t.Errorf("validTarget(%q) succeeded, want error", host)
		}
	}
	if err := validTarget(&store.ProbeTarget{Name: "x", Host: "1.1.1.1", Type: "tcp", Port: 443}); err != nil {
		t.Errorf("validTarget(1.1.1.1) failed: %v", err)
	}

	// installScriptBody escapes sign_pubkey safely.
	a.Script = []byte(`PUBKEY="__SIGN_PUBKEY__"`)
	_ = a.P.St.Set("sign_pubkey", `abc"$(id)`+"\nrm -rf /")
	gotScript := string(a.installScriptBody())
	if strings.Contains(gotScript, "\n") || strings.Contains(gotScript, `$(id)`) && !strings.Contains(gotScript, `\$(id)`) {
		t.Fatalf("installScriptBody did not safely escape sign_pubkey: %q", gotScript)
	}

	// CSV formula prefix neutralization.
	a.P.St.DB.Create(&store.AuditLog{Type: "system", Action: "=cmd", Target: "+1", Detail: "-2", IP: "@evil", TS: time.Now()})
	w := doReq(r, http.MethodGet, "/api/audit?format=csv", "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/audit?format=csv = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"'=cmd", "'+1", "'-2", "'@evil"} {
		if !strings.Contains(body, want) {
			t.Errorf("CSV missing sanitized cell %q in:\n%s", want, body)
		}
	}

	// ack-all acknowledges "info" events too.
	now := time.Now()
	infoEv := &store.AlertEvent{Kind: "ip_changed", Status: "info", Text: "ip changed", StartedAt: now, ResolvedAt: &now}
	a.P.St.DB.Create(infoEv)
	w = doReq(r, http.MethodPost, "/api/alerts/events/ack-all", "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ack-all = %d", w.Code)
	}
	var reloaded store.AlertEvent
	a.P.St.DB.First(&reloaded, infoEv.ID)
	if !reloaded.Acked {
		t.Fatal("info alert event was not acknowledged by ack-all")
	}

	// getConfig & diffConfig transparently decrypt sealed ConfigVersion and redact secrets.
	var cv store.ConfigVersion
	if err := a.P.St.DB.Where("server_id = ?", 1).Order("version DESC").First(&cv).Error; err != nil {
		t.Fatal(err)
	}
	w = doReq(r, http.MethodGet, "/api/servers/1/configs/"+strconv.FormatUint(uint64(cv.ID), 10), "panel.example.com", sid, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "reality-in") || !strings.Contains(w.Body.String(), "••••••") {
		t.Fatalf("GET config = %d %s", w.Code, w.Body.String())
	}

	// MaxBytesReader rejects oversized JSON request (> 4 MiB).
	big := bytes.Repeat([]byte("a"), (4<<20)+1024)
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(big))
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sid})
	req.Header.Set("X-Requested-With", "vps-panel")
	req.Header.Set("Content-Type", "application/json")
	wBig := httptest.NewRecorder()
	r.ServeHTTP(wBig, req)
	if wBig.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", wBig.Code)
	}
}
