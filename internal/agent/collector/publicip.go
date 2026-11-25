package collector

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"time"
)

type ipEndpoint struct {
	url   string
	trace bool // Cloudflare cdn-cgi/trace format ("ip=...")
}

var (
	publicIPEndpoints = []ipEndpoint{
		{url: "https://1.1.1.1/cdn-cgi/trace", trace: true},
		{url: "https://checkip.amazonaws.com"},
		{url: "https://api4.ipify.org"},
	}
	publicIPTransport = &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     15 * time.Second,
	}
	publicIPClient = &http.Client{Timeout: 5 * time.Second, Transport: publicIPTransport}
)

// PublicIPv4 asks Cloudflare's trace endpoint and falls back to plain-text IPv4
// echo services when 1.1.1.1 is unreachable.
func PublicIPv4() string {
	defer publicIPTransport.CloseIdleConnections()
	for _, ep := range publicIPEndpoints {
		if ip := fetchPublicIPv4(ep); ip != "" {
			return ip
		}
	}
	return ""
}

func fetchPublicIPv4(ep ipEndpoint) string {
	resp, err := publicIPClient.Get(ep.url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	if ep.trace {
		sc := bufio.NewScanner(io.LimitReader(resp.Body, 4096))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
				v = strings.TrimSpace(v)
				if isPublicIPv4(v) {
					return v
				}
			}
		}
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(b))
	if isPublicIPv4(ip) {
		return ip
	}
	return ""
}
