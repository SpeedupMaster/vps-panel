package tasks

import (
	"net"
	"net/http"
	"testing"
)

func TestCDNFromCNAME(t *testing.T) {
	cases := map[string]string{
		"e6858.dscx.akamaiedge.net.":         "Akamai",
		"www.apple.com.edgekey.net":          "Akamai",
		"d3ag4hukkh62yn.cloudfront.net.":     "CloudFront",
		"dualstack.n.sni.global.fastly.net":  "Fastly",
		"www.example.com.cdn.cloudflare.net": "Cloudflare",
		"www.example.com.":                   "",
		"notakamai.net.":                     "",
	}
	for in, want := range cases {
		if got := cdnFromCNAME(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestCDNFromIP(t *testing.T) {
	cases := map[string]string{"104.16.123.96": "Cloudflare", "172.67.1.1": "Cloudflare", "2606:4700::6810:7b60": "Cloudflare",
		"151.101.1.91": "Fastly", "17.253.144.10": "", "124.83.190.124": "", "8.8.8.8": ""}
	for ip, want := range cases {
		if got := cdnFromIP(net.ParseIP(ip)); got != want {
			t.Errorf("%s: got %q want %q", ip, got, want)
		}
	}
}

func TestCDNFromHeaders(t *testing.T) {
	h := func(kv ...string) http.Header {
		x := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			x.Set(kv[i], kv[i+1])
		}
		return x
	}
	cases := []struct {
		h    http.Header
		want string
	}{
		{h("Server", "cloudflare", "Cf-Ray", "a5cf1b84385ff6ef-NRT"), "Cloudflare"},
		{h("Server", "CloudFront", "X-Amz-Cf-Id", "x"), "CloudFront"},
		{h("Server", "AmazonS3", "Via", "1.1 abc.cloudfront.net (CloudFront)"), "CloudFront"},
		{h("Server", "AkamaiGHost"), "Akamai"},
		{h("Server", "AkamaiNetStorage"), "Akamai"},
		{h("X-Served-By", "cache-bfi-kbfi7400049-BFI", "Via", "1.1 varnish"), "Fastly"},
		{h("X-Served-By", "marketing-site"), ""},
		{h("Server", "Apple"), ""},
		{h("Server", "nginx"), ""},
	}
	for _, c := range cases {
		if got, _ := cdnFromHeaders(c.h); got != c.want {
			t.Errorf("%v: got %q want %q", c.h, got, c.want)
		}
	}
}
