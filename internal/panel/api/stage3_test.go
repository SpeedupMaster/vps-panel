package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestShellTicketsIsolatedPerAPIInstance(t *testing.T) {
	a1, _ := newTestAPI(t, false)
	a2, sid2 := newTestAPI(t, false)

	a1.ticketsMu.Lock()
	a1.tickets = map[string]shellTicket{
		"shared-ticket": {server: 1, exp: time.Now().Add(time.Minute)},
	}
	a1.ticketsMu.Unlock()

	r2 := a2.Router()
	w := doReq(r2, http.MethodGet, "/api/servers/1/shell?ticket=shared-ticket", "panel.example.com", sid2, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a2 accepted a1's shell ticket: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteScriptReferencedReturns400(t *testing.T) {
	a, sid := newTestAPI(t, false)
	r := a.Router()

	w := doReq(r, http.MethodPost, "/api/scripts", "panel.example.com", sid, map[string]any{
		"name": "my-script", "content": "echo hi",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/scripts = %d %s", w.Code, w.Body.String())
	}
	var sc struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sc); err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatUint(uint64(sc.ID), 10)

	w = doReq(r, http.MethodPost, "/api/schedules", "panel.example.com", sid, map[string]any{
		"name": "run-my-script", "cron": "@every 1h", "action": "script:" + idStr, "servers": "all", "enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/schedules = %d %s", w.Code, w.Body.String())
	}

	w = doReq(r, http.MethodDelete, "/api/scripts/"+idStr, "panel.example.com", sid, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE referenced script = %d, want 400", w.Code)
	}
}
