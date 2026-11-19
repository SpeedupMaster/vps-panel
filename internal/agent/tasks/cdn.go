package tasks

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// CDN detection for Reality targets. Reality forwards every unauthenticated
// connection to the target, so a target on a shared CDN turns the VPS into a
// free relay for that CDN: scanners that collect "CDN front" IPs route other
// sites' traffic through it and burn the VPS's bandwidth.

// cdnCNAMEs maps a canonical-name suffix to its CDN.
var cdnCNAMEs = []struct{ suffix, name string }{
	{".akamaiedge.net", "Akamai"}, {".akamai.net", "Akamai"}, {".edgekey.net", "Akamai"}, {".edgesuite.net", "Akamai"},
	{".akamaized.net", "Akamai"}, {".akamaihd.net", "Akamai"},
	{".cloudfront.net", "CloudFront"},
	{".fastly.net", "Fastly"}, {".fastlylb.net", "Fastly"},
	{".cdn.cloudflare.net", "Cloudflare"},
	{".azureedge.net", "Azure CDN"}, {".azurefd.net", "Azure Front Door"}, {".t-msedge.net", "Azure Front Door"},
	{".s-msedge.net", "Azure Front Door"}, {".msecnd.net", "Azure CDN"},
	{".edgecastcdn.net", "Edgio"}, {".llnwd.net", "Edgio"}, {".systemcdn.net", "Edgio"},
	{".footprint.net", "Lumen CDN"}, {".cdn77.org", "CDN77"}, {".b-cdn.net", "Bunny CDN"}, {".kxcdn.com", "KeyCDN"},
	{".gcdn.co", "Gcore"}, {".cdnetworks.net", "CDNetworks"}, {".incapdns.net", "Imperva"}, {".stackpathdns.com", "StackPath"},
	{".vercel-dns.com", "Vercel"}, {".netlifyglobalcdn.com", "Netlify"},
	{".alikunlun.com", "阿里云 CDN"}, {".kunlunca.com", "阿里云 CDN"}, {".cdngslb.com", "阿里云 CDN"},
	{".tdnsv5.com", "腾讯云 CDN"}, {".cdn.dnsv1.com", "腾讯云 CDN"}, {".wscdns.com", "网宿"}, {".wsglb0.com", "网宿"},
	{".qiniudns.com", "七牛 CDN"}, {".bdydns.com", "百度云 CDN"},
}

// Published ranges of the two CDNs whose customers often have no telling
// CNAME (cloudflare.com/ips, api.fastly.com/public-ip-list).
type cdnNet struct {
	n    *net.IPNet
	name string
}

var cdnNets = func() []cdnNet {
	var out []cdnNet
	add := func(name string, cidrs ...string) {
		for _, c := range cidrs {
			_, n, _ := net.ParseCIDR(c)
			out = append(out, cdnNet{n, name})
		}
	}
	add("Cloudflare", "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
		"190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32")
	add("Fastly", "151.101.0.0/16", "199.232.0.0/16", "146.75.0.0/17", "2a04:4e40::/32", "2a04:4e42::/32")
	return out
}()

// cdnFromCNAME returns the CDN a canonical name belongs to.
func cdnFromCNAME(cname string) string {
	c := "." + strings.ToLower(strings.TrimSuffix(cname, "."))
	for _, s := range cdnCNAMEs {
		if strings.HasSuffix(c, s.suffix) {
			return s.name
		}
	}
	return ""
}

// cdnFromIP returns the CDN whose published range holds ip.
func cdnFromIP(ip net.IP) string {
	for _, c := range cdnNets {
		if c.n.Contains(ip) {
			return c.name
		}
	}
	return ""
}

// cdnFromHeaders recognizes the headers CDN edges add; it returns the CDN
// and the header that gave it away.
func cdnFromHeaders(h http.Header) (string, string) {
	low := func(k string) string { return strings.ToLower(h.Get(k)) }
	server := low("Server")
	switch {
	case h.Get("Cf-Ray") != "" || server == "cloudflare":
		return "Cloudflare", "cf-ray"
	case h.Get("X-Amz-Cf-Id") != "" || h.Get("X-Amz-Cf-Pop") != "" || strings.Contains(low("Via"), "cloudfront") || server == "cloudfront":
		return "CloudFront", "x-amz-cf-id"
	case strings.HasPrefix(server, "akamai") || h.Get("Akamai-Grn") != "" || h.Get("Akamai-Cache-Status") != "" || h.Get("Akamai-Request-Bc") != "" || strings.HasPrefix(low("X-Cdn"), "akam"):
		return "Akamai", "server / akamai-*"
	case h.Get("X-Fastly-Request-Id") != "" || strings.HasPrefix(low("X-Served-By"), "cache-"):
		return "Fastly", "x-served-by"
	case h.Get("X-Azure-Ref") != "" || h.Get("X-Msedge-Ref") != "":
		return "Azure Front Door", "x-azure-ref"
	case h.Get("X-Vercel-Id") != "":
		return "Vercel", "x-vercel-id"
	case h.Get("X-Nf-Request-Id") != "":
		return "Netlify", "x-nf-request-id"
	case strings.HasPrefix(server, "bunnycdn"):
		return "Bunny CDN", "server"
	case strings.HasPrefix(server, "cdn77"):
		return "CDN77", "server"
	case h.Get("X-Iinfo") != "" || strings.Contains(low("X-Cdn"), "imperva") || strings.Contains(low("X-Cdn"), "incapsula"):
		return "Imperva", "x-iinfo"
	case h.Get("X-Sucuri-Id") != "":
		return "Sucuri", "x-sucuri-id"
	case h.Get("Eagleid") != "" || h.Get("Ali-Swift-Global-Savetime") != "":
		return "阿里云 CDN", "eagleid"
	case h.Get("X-Nws-Log-Uuid") != "":
		return "腾讯云 CDN", "x-nws-log-uuid"
	case h.Get("X-Ws-Request-Id") != "":
		return "网宿", "x-ws-request-id"
	case strings.Contains(low("Via"), "google"):
		return "Google Cloud CDN", "via"
	}
	return "", ""
}

// detectCDN checks the target's CNAME chain, its addresses and the response
// headers. It returns the CDN name and the evidence, or "" when none matched.
func detectCDN(ctx context.Context, domain string, h http.Header) (string, string) {
	if cname, err := net.DefaultResolver.LookupCNAME(ctx, domain); err == nil {
		if n := cdnFromCNAME(cname); n != "" {
			return n, "CNAME " + strings.TrimSuffix(cname, ".")
		}
	}
	if ips, err := net.DefaultResolver.LookupIPAddr(ctx, domain); err == nil {
		for _, ip := range ips {
			if n := cdnFromIP(ip.IP); n != "" {
				return n, "IP " + ip.IP.String()
			}
		}
	}
	if h != nil {
		if n, by := cdnFromHeaders(h); n != "" {
			return n, "响应头 " + by
		}
	}
	return "", ""
}
