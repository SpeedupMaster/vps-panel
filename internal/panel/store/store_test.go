package store

import "testing"

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

	if cacheSize != -2000 {
		t.Errorf("cache_size = %d, want -2000", cacheSize)
	}
	if tempStore != 1 { // 1 = FILE
		t.Errorf("temp_store = %d, want 1 (FILE)", tempStore)
	}
	if journalLimit != 8388608 {
		t.Errorf("journal_size_limit = %d, want 8388608", journalLimit)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
}
