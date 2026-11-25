package prober

import (
	"reflect"
	"testing"
	"time"
)

func TestSummarizeJitterBeforeSort(t *testing.T) {
	in := []float64{10, 100, 10, 100, 10}
	orig := append([]float64(nil), in...)
	s := Summarize("target-1", in, 5)

	if s.Jitter != 90 {
		t.Fatalf("Jitter = %v, want 90", s.Jitter)
	}
	if s.Avg != 46 || s.Min != 10 || s.Max != 100 || s.Loss != 0 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("Summarize mutated input slice: got %v, want %v", in, orig)
	}
}

func TestSummarizeEdgeCases(t *testing.T) {
	if s := Summarize("a", []float64{25.5}, 2); s.Jitter != 0 || s.Avg != 25.5 || s.Min != 25.5 || s.Max != 25.5 || s.Loss != 0.5 {
		t.Fatalf("single sample: %+v", s)
	}
	if s := Summarize("b", nil, 5); s.Avg != -1 || s.Loss != 1 || s.Jitter != 0 {
		t.Fatalf("all lost: %+v", s)
	}
	if s := Summarize("c", nil, 0); s.Avg != -1 || s.Loss != 1 {
		t.Fatalf("zero sent: %+v", s)
	}
}

func TestICMPPingRejectsLeadingDash(t *testing.T) {
	if _, err := ICMPPing("-c", time.Second); err == nil {
		t.Fatal("expected leading-dash host to be rejected")
	}
	if _, err := ICMPPing("", time.Second); err == nil {
		t.Fatal("expected empty host to be rejected")
	}
}
