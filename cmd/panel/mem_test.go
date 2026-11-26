package main

import "testing"

func TestParseAndComputeMemoryLimit(t *testing.T) {
	if n, ok := parseCgroupLimit("max\n"); ok || n != 0 {
		t.Fatalf("max = (%d, %v), want (0, false)", n, ok)
	}
	if n, ok := parseCgroupLimit("9223372036854771712\n"); ok || n != 0 {
		t.Fatalf("huge cgroup v1 sentinel = (%d, %v), want (0, false)", n, ok)
	}
	if n, ok := parseCgroupLimit("268435456\n"); !ok || n != 256<<20 {
		t.Fatalf("256MiB = (%d, %v), want (%d, true)", n, ok, 256<<20)
	}

	// Uncapped host defaults to 64 MiB so Go heap stays compact on small VPSes.
	if got := computeMemoryLimit(0, false); got != 64<<20 {
		t.Fatalf("uncapped limit = %d, want %d", got, 64<<20)
	}
	// 256 MiB container -> 80% (~204.8 MiB).
	if got := computeMemoryLimit(256<<20, true); got != (256<<20)*4/5 {
		t.Fatalf("256MiB container limit = %d, want %d", got, (256<<20)*4/5)
	}
	// Very tight 32 MiB container -> clamped to floor 48 MiB? No, must not exceed cgroup; clamp floor only when cgroup >= 48MiB.
	if got := computeMemoryLimit(32<<20, true); got != (32<<20)*4/5 {
		t.Fatalf("32MiB container limit = %d, want %d", got, (32<<20)*4/5)
	}
}
