package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestFailuresMapEvictsExpiredEntries(t *testing.T) {
	a := New(nil, nil)
	expired := time.Now().Add(-lockFor - time.Minute)

	// 1. locked() deletes the key instead of storing an empty slice.
	a.failures["1.2.3.4"] = []time.Time{expired}
	if a.locked("1.2.3.4") {
		t.Fatal("expired IP should not be locked")
	}
	if _, exists := a.failures["1.2.3.4"]; exists {
		t.Fatal("expired IP key was not deleted from failures map")
	}

	// 2. High-cardinality expired IPs are pruned when new failures arrive.
	for i := 0; i < 300; i++ {
		a.failures[fmt.Sprintf("10.0.%d.%d", i/256, i%256)] = []time.Time{expired}
	}
	a.fail("203.0.113.1")
	if got := len(a.failures); got != 1 {
		t.Fatalf("failures map retained %d stale keys, want 1", got)
	}
}
