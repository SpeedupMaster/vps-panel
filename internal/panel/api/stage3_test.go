package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/panel/store"
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

func TestProbeSeriesEndpointBatchesTargetsInSingleQuery(t *testing.T) {
	a, sid := newTestAPI(t, false)
	r := a.Router()

	now := time.Now().Unix()
	rows := []store.ProbeResult{
		{ServerID: 1, Target: "zs-bj", TS: now - 120, Kind: "backhaul", Avg: 31.5, Loss: 0},
		{ServerID: 1, Target: "zs-bj", TS: now - 60, Kind: "backhaul", Avg: 32.5, Loss: 0},
		{ServerID: 1, Target: "zs-sh", TS: now - 60, Kind: "backhaul", Avg: 41.0, Loss: 10},
		{ServerID: 1, Target: "zs-gd", TS: now - 60, Kind: "backhaul", Avg: 52.0, Loss: 0},
	}
	if err := a.P.St.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	var queries atomic.Int64
	const cbName = "test:count_probe_series_queries"
	if err := a.P.St.R().Callback().Query().After("gorm:query").Register(cbName, func(db *gorm.DB) {
		if db.Statement != nil && strings.Contains(db.Statement.SQL.String(), "probe_results") {
			queries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}

	w := doReq(r, http.MethodGet, "/api/probes/series?server_id=1&range=86400&targets=zs-bj,zs-sh,zs-gd,zs-empty", "panel.example.com", sid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/probes/series = %d %s", w.Code, w.Body.String())
	}
	var body map[string][]store.ProbeResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body["zs-bj"]) != 2 || body["zs-bj"][0].Avg != 31.5 || body["zs-bj"][1].Avg != 32.5 {
		t.Fatalf("zs-bj = %+v", body["zs-bj"])
	}
	if len(body["zs-sh"]) != 1 || body["zs-sh"][0].Avg != 41.0 {
		t.Fatalf("zs-sh = %+v", body["zs-sh"])
	}
	if len(body["zs-gd"]) != 1 || body["zs-gd"][0].Avg != 52.0 {
		t.Fatalf("zs-gd = %+v", body["zs-gd"])
	}
	if empty, exists := body["zs-empty"]; !exists || len(empty) != 0 {
		t.Fatalf("zs-empty = %+v (exists=%v), want empty slice", empty, exists)
	}
	if got := queries.Load(); got != 1 {
		t.Fatalf("probe_results SELECT queries = %d, want 1 batched query", got)
	}
}
