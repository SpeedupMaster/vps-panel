package notify

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSafeHTTPClientBlocksSSRFAndCrossHostRedirect(t *testing.T) {
	// Blocked IP checks.
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "172.16.5.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "fe80::1"} {
		if !IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("IsBlockedIP(%s) = false, want true", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("IsBlockedIP(%s) = true, want false", s)
		}
	}

	// Outbound request to loopback httptest server is blocked by default.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := Send(context.Background(), "webhook", map[string]string{"url": srv.URL}, Message{Title: "t"}); err == nil {
		t.Fatal("SSRF to 127.0.0.1 succeeded")
	}

	// With test hook enabled, cross-host redirect must STILL be blocked.
	AllowPrivateIPsForTest = true
	defer func() { AllowPrivateIPsForTest = false }()

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer redir.Close()

	c := SafeHTTPClient(5 * time.Second)
	resp, err := c.Get(redir.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("cross-host redirect was not blocked")
	}
	if !strings.Contains(err.Error(), "跨主机重定向") {
		t.Fatalf("unexpected error for cross-host redirect: %v", err)
	}
}

func TestTruncateRunesUTF8(t *testing.T) {
	in := strings.Repeat("告警通知", 1500)
	got := truncateRunes(in, 4000)
	if !utf8.ValidString(got) {
		t.Fatal("truncateRunes produced invalid UTF-8")
	}
	if utf8.RuneCountInString(got) != 4000 {
		t.Fatalf("rune count = %d, want 4000", utf8.RuneCountInString(got))
	}
}
