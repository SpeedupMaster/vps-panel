package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestOpenConfiguresMemoryPragmas(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var cacheSize, tempStore, journalLimit int64
	var journalMode string
	st.DB.Raw("PRAGMA cache_size").Scan(&cacheSize)
	st.DB.Raw("PRAGMA temp_store").Scan(&tempStore)
	st.DB.Raw("PRAGMA journal_size_limit").Scan(&journalLimit)
	st.DB.Raw("PRAGMA journal_mode").Scan(&journalMode)

	if cacheSize != -8000 {
		t.Errorf("DB cache_size = %d, want -8000", cacheSize)
	}
	if tempStore != 1 { // 1 = FILE
		t.Errorf("DB temp_store = %d, want 1 (FILE)", tempStore)
	}
	if journalLimit != 8388608 {
		t.Errorf("DB journal_size_limit = %d, want 8388608", journalLimit)
	}
	if journalMode != "wal" {
		t.Errorf("DB journal_mode = %q, want wal", journalMode)
	}

	if st.ReadDB == nil {
		t.Fatal("ReadDB was not initialized")
	}
	var rCache, rTemp, rQueryOnly int64
	var rJournal string
	st.R().Raw("PRAGMA cache_size").Scan(&rCache)
	st.R().Raw("PRAGMA temp_store").Scan(&rTemp)
	st.R().Raw("PRAGMA query_only").Scan(&rQueryOnly)
	st.R().Raw("PRAGMA journal_mode").Scan(&rJournal)
	if rCache != -8000 {
		t.Errorf("ReadDB cache_size = %d, want -8000", rCache)
	}
	if rTemp != 2 { // 2 = MEMORY
		t.Errorf("ReadDB temp_store = %d, want 2 (MEMORY)", rTemp)
	}
	if rQueryOnly != 1 {
		t.Errorf("ReadDB query_only = %d, want 1", rQueryOnly)
	}
	if rJournal != "wal" {
		t.Errorf("ReadDB journal_mode = %q, want wal", rJournal)
	}
}

func TestReadWritePoolConcurrencyAndIndexes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// ReadDB must reject writes (query_only=1).
	if err := st.R().Create(&Setting{Key: "ro_test", Value: "1"}).Error; err == nil {
		t.Fatal("ReadDB unexpectedly allowed write")
	}

	// Concurrent writer on DB + multiple readers on ReadDB must not fail with SQLITE_BUSY.
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			if err := st.DB.Create(&AuditLog{
				Type: "system", Action: "write", Target: fmt.Sprintf("t-%d", i), TS: time.Now(),
			}).Error; err != nil {
				errCh <- err
				return
			}
		}
	}()
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				var n int64
				if err := st.R().Model(&AuditLog{}).Count(&n).Error; err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent read/write error: %v", err)
	}

	// Unique composite index on ConfigVersion (server_id, version).
	if err := st.DB.Create(&ConfigVersion{ServerID: 1, Version: 1, Config: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.DB.Create(&ConfigVersion{ServerID: 1, Version: 1, Config: "b"}).Error; err == nil {
		t.Fatal("duplicate (server_id, version) in config_versions was allowed")
	}

	// Verify key Stage 2 indexes exist in sqlite_master.
	var indexes []string
	st.R().Raw("SELECT name FROM sqlite_master WHERE type = 'index'").Scan(&indexes)
	has := map[string]bool{}
	for _, idx := range indexes {
		has[idx] = true
	}
	for _, want := range []string{
		"idx_metrics_raw_ts",
		"idx_metrics_1m_ts",
		"idx_metrics_1h_ts",
		"idx_probe_results_ts",
		"idx_probe_results_1h_ts",
		"idx_sessions_expires_at",
		"idx_tasks_status",
		"idx_tasks_finished_at",
		"idx_alert_events_status",
		"idx_alert_events_started_at",
		"idx_sub_access_token_ts",
		"idx_config_ver_server_version",
	} {
		if !has[want] {
			t.Errorf("missing expected index %q (have: %v)", want, indexes)
		}
	}
}
