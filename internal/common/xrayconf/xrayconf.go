// Package xrayconf renders Xray configs for entry (Reality) and landing
// (SS2022) machines. It is pure: the panel feeds it data, the output is
// validated on the VPS with `xray run -test`.
package xrayconf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

const APIPort = 10085

// Reality is the single VLESS+Reality inbound of an entry machine.
type Reality struct {
	Port        int
	Target      string // host:port
	ServerNames []string
	PrivateKey  string
	ShortIDs    []string
}

// User is one route: its email selects the outbound in routing.
type User struct {
	Email    string
	UUID     string
	Outbound string // outbound tag, or
	Balancer string // balancer tag (route with backup landings)
}

// SSOut is a chain hop to a landing machine.
type SSOut struct {
	Tag      string
	Address  string
	Port     int
	Method   string
	Password string // serverKey:userKey
}

type Balancer struct {
	Tag      string
	Selector []string // outbound tag prefixes
	Fallback string   // exact outbound tag used before observatory has data
}

// Landing is the SS2022 multi-user inbound of a landing machine.
type Landing struct {
	Port      int
	Method    string
	ServerKey string
	Clients   []SSClient
}

type SSClient struct {
	Email    string
	Password string
}

type Node struct {
	XrayVersion string
	Reality     *Reality
	Users       []User
	Outs        []SSOut
	Balancers   []Balancer
	Landing     *Landing
	AccessLog   bool
	BlockBT     bool
	ProbeURL    string
}

type obj = map[string]any

// Build renders compact JSON.
func Build(n Node) ([]byte, error) {
	access := "none"
	if n.AccessLog {
		access = ""
	}
	cfg := obj{
		"log":   obj{"loglevel": "warning", "access": access},
		"api":   obj{"tag": "api", "services": []string{"HandlerService", "StatsService", "RoutingService"}},
		"stats": obj{},
		"policy": obj{
			"levels": obj{"0": obj{"statsUserUplink": true, "statsUserDownlink": true}},
			"system": obj{"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true},
		},
	}
	inbounds := []any{obj{
		"tag": "api", "listen": "127.0.0.1", "port": APIPort, "protocol": "dokodemo-door",
		"settings": obj{"address": "127.0.0.1"},
	}}
	rules := []any{obj{"inboundTag": []string{"api"}, "outboundTag": "api"}}

	if r := n.Reality; r != nil {
		clients := []any{}
		for _, u := range n.Users {
			clients = append(clients, obj{"id": u.UUID, "flow": "xtls-rprx-vision", "email": u.Email})
		}
		rs := obj{"show": false, "serverNames": r.ServerNames, "privateKey": r.PrivateKey, "shortIds": r.ShortIDs}
		if SupportsTarget(n.XrayVersion) {
			rs["target"] = r.Target
		} else {
			rs["dest"] = r.Target
		}
		inbounds = append(inbounds, obj{
			"tag": "reality-in", "port": r.Port, "protocol": "vless",
			"settings":       obj{"decryption": "none", "clients": clients},
			"streamSettings": obj{"network": "tcp", "security": "reality", "realitySettings": rs},
			"sniffing":       obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
		})
	}
	if l := n.Landing; l != nil {
		clients := []any{}
		for _, c := range l.Clients {
			clients = append(clients, obj{"password": c.Password, "email": c.Email})
		}
		inbounds = append(inbounds, obj{
			"tag": "ss-in", "port": l.Port, "protocol": "shadowsocks",
			"settings": obj{"method": l.Method, "password": l.ServerKey, "clients": clients, "network": "tcp,udp"},
			"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
		})
	}
	cfg["inbounds"] = inbounds

	outbounds := []any{obj{"tag": "direct", "protocol": "freedom"}}
	for _, o := range n.Outs {
		outbounds = append(outbounds, obj{
			"tag": o.Tag, "protocol": "shadowsocks",
			"settings": obj{"servers": []any{obj{"address": o.Address, "port": o.Port, "method": o.Method, "password": o.Password}}},
		})
	}
	outbounds = append(outbounds, obj{"tag": "block", "protocol": "blackhole"})
	cfg["outbounds"] = outbounds

	rules = append(rules,
		obj{"domain": []string{"localhost", "domain:localhost", "full:metadata.google.internal"}, "outboundTag": "block"},
		obj{"ip": []string{"geoip:private"}, "outboundTag": "block"},
	)
	if n.BlockBT {
		rules = append(rules, obj{"protocol": []string{"bittorrent"}, "outboundTag": "block"})
	}
	// One rule per user, tagged with its email, so a route can be added or
	// removed at runtime (xray api adrules / rmrules) without a restart.
	for _, u := range n.Users {
		rules = append(rules, UserRule(u))
	}
	routing := obj{"domainStrategy": "IPIfNonMatch", "rules": rules}
	if len(n.Balancers) > 0 {
		var bs []any
		var subjects []string
		for _, b := range n.Balancers {
			bs = append(bs, obj{"tag": b.Tag, "selector": b.Selector, "strategy": obj{"type": "leastPing"}, "fallbackTag": b.Fallback})
			subjects = append(subjects, b.Selector...)
		}
		routing["balancers"] = bs
		probe := n.ProbeURL
		if probe == "" {
			probe = "https://www.gstatic.com/generate_204"
		}
		cfg["observatory"] = obj{"subjectSelector": dedupe(subjects), "probeURL": probe, "probeInterval": "1m", "enableConcurrency": true}
		// ObservatoryService only resolves when an observatory exists; Xray
		// refuses to start ("not all dependencies are resolved") otherwise.
		api := cfg["api"].(obj)
		api["services"] = append(api["services"].([]string), "ObservatoryService")
	}
	cfg["routing"] = routing
	return json.Marshal(cfg)
}

// UserRule is the routing rule for one route user.
func UserRule(u User) map[string]any {
	r := obj{"ruleTag": u.Email, "user": []string{u.Email}}
	if u.Balancer != "" {
		r["balancerTag"] = u.Balancer
	} else {
		r["outboundTag"] = u.Outbound
	}
	return r
}

// SupportsTarget: Reality "dest" was renamed "target" in Xray 25.x; older
// versions only understand "dest".
func SupportsTarget(v string) bool {
	if v == "" {
		return true
	}
	return compareVersion(v, "25.1.1") >= 0
}

// SupportsX25519MLKEM768 reports whether the Xray version supports the
// X25519MLKEM768 post-quantum key share in Reality handshakes (introduced in
// Xray 25.5.16 and required by Xray 26.9.8+).
func SupportsX25519MLKEM768(v string) bool {
	if v == "" {
		return true
	}
	return compareVersion(v, "25.5.16") >= 0
}

func splitVersion(v string) (nums [3]int, pre string) {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if core, _, ok := strings.Cut(s, "+"); ok {
		s = core
	}
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		p := parts[i]
		// Take leading decimal digits so suffixes without '-' still parse cleanly.
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if j > 0 {
			nums[i], _ = strconv.Atoi(p[:j])
		}
	}
	return nums, pre
}

func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // release > pre-release
	}
	if b == "" {
		return -1
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	n := min(len(pa), len(pb))
	for i := 0; i < n; i++ {
		if pa[i] == pb[i] {
			continue
		}
		ai, aErr := strconv.Atoi(pa[i])
		bi, bErr := strconv.Atoi(pb[i])
		switch {
		case aErr == nil && bErr == nil:
			if ai < bi {
				return -1
			}
			return 1
		case aErr == nil:
			return -1 // numeric identifiers have lower precedence than non-numeric
		case bErr == nil:
			return 1
		case pa[i] < pb[i]:
			return -1
		default:
			return 1
		}
	}
	if len(pa) < len(pb) {
		return -1
	}
	if len(pa) > len(pb) {
		return 1
	}
	return 0
}

func compareVersion(a, b string) int {
	na, preA := splitVersion(a)
	nb, preB := splitVersion(b)
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	return comparePreRelease(preA, preB)
}

// CompareVersion is exported for update checks.
func CompareVersion(a, b string) int { return compareVersion(a, b) }

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Pretty indents compact JSON the same way on panel and agent so hashes match.
func Pretty(b []byte) []byte {
	var buf bytes.Buffer
	if json.Indent(&buf, bytes.TrimSpace(b), "", "  ") == nil {
		return append(buf.Bytes(), '\n')
	}
	return b
}

// Hash identifies a config file on disk (of Pretty output).
func Hash(pretty []byte) string {
	s := sha256.Sum256(pretty)
	return hex.EncodeToString(s[:8])
}
