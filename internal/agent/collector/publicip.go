package collector

import (
	"bufio"
	"net/http"
	"strings"
	"time"
)

// PublicIPv4 asks Cloudflare's trace endpoint; used when the outbound
// interface only has a private address (NAT VPS).
func PublicIPv4() string {
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get("https://1.1.1.1/cdn-cgi/trace")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			return v
		}
	}
	return ""
}
