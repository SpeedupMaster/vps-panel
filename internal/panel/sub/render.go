// Package sub renders subscriptions (mihomo YAML, sing-box JSON, Base64
// share links) from a list of Reality nodes. Everything happens inside the
// panel; no third-party converter is involved.
package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Node struct {
	Name       string
	Server     string
	Port       int
	UUID       string
	SNI        string
	PublicKey  string
	ShortID    string
	FP         string
	RegionCode string // landing country for chains, entry country for direct
}

// Flag converts an ISO country code to its flag emoji.
func Flag(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return "🏳"
	}
	return string(rune(0x1F1E6+int(cc[0]-'A'))) + string(rune(0x1F1E6+int(cc[1]-'A')))
}

// VLESS share link (v2rayN / Shadowrocket / NekoBox format).
func Link(n Node) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("flow", "xtls-rprx-vision")
	q.Set("security", "reality")
	q.Set("sni", n.SNI)
	q.Set("fp", n.FP)
	q.Set("pbk", n.PublicKey)
	q.Set("sid", n.ShortID)
	q.Set("type", "tcp")
	q.Set("headerType", "none")
	host := n.Server
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", n.UUID, host, n.Port, q.Encode(), url.PathEscape(n.Name))
}

func Base64(nodes []Node) string {
	lines := make([]string, len(nodes))
	for i, n := range nodes {
		lines[i] = Link(n)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
}

const (
	GroupSelect   = "节点选择"
	GroupAuto     = "自动选择"
	GroupFallback = "故障转移"
	TestURL       = "https://www.gstatic.com/generate_204"
)

// regions groups node names by region code, sorted by code.
func regions(nodes []Node) ([]string, map[string][]string) {
	m := map[string][]string{}
	for _, n := range nodes {
		cc := strings.ToUpper(n.RegionCode)
		if cc == "" {
			cc = "OT"
		}
		m[cc] = append(m[cc], n.Name)
	}
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes, m
}

func regionGroupName(cc string) string { return Flag(cc) + " " + cc }

// expand replaces the {{all}} / {{regions}} / {{groups}} tokens in a proxy list.
func expand(list []string, all, regionNames []string) []string {
	var out []string
	for _, x := range list {
		switch x {
		case "{{all}}":
			out = append(out, all...)
		case "{{regions}}":
			out = append(out, regionNames...)
		case "{{groups}}":
			out = append(out, GroupSelect, GroupAuto, GroupFallback)
			out = append(out, regionNames...)
		default:
			out = append(out, x)
		}
	}
	return out
}

// ---------- mihomo ----------

func mihomoProxy(n Node) map[string]any {
	return map[string]any{
		"name": n.Name, "type": "vless", "server": n.Server, "port": n.Port, "uuid": n.UUID,
		"network": "tcp", "tls": true, "udp": true, "flow": "xtls-rprx-vision",
		"servername": n.SNI, "client-fingerprint": n.FP,
		"reality-opts": map[string]any{"public-key": n.PublicKey, "short-id": n.ShortID},
	}
}

// Mihomo renders a Clash Meta config. The template supplies everything but
// proxies and the three generated groups; its own groups may use the
// {{all}} / {{regions}} / {{groups}} tokens inside `proxies`.
func Mihomo(nodes []Node, tpl string) ([]byte, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultMihomo
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(tpl), &doc); err != nil {
		return nil, fmt.Errorf("模板不是合法 YAML：%w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("模板顶层必须是映射")
	}
	root := doc.Content[0]

	all := make([]string, len(nodes))
	proxies := make([]any, len(nodes))
	for i, n := range nodes {
		all[i] = n.Name
		proxies[i] = mihomoProxy(n)
	}
	codes, byRegion := regions(nodes)
	var regionNames []string
	for _, c := range codes {
		regionNames = append(regionNames, regionGroupName(c))
	}
	if len(all) == 0 {
		all = []string{"DIRECT"}
	}
	gen := []any{
		map[string]any{"name": GroupSelect, "type": "select", "proxies": append(append([]string{GroupAuto, GroupFallback}, regionNames...), append(all, "DIRECT")...)},
		map[string]any{"name": GroupAuto, "type": "url-test", "url": TestURL, "interval": 300, "tolerance": 50, "lazy": true, "proxies": all},
		map[string]any{"name": GroupFallback, "type": "fallback", "url": TestURL, "interval": 300, "lazy": true, "proxies": all},
	}
	for _, c := range codes {
		gen = append(gen, map[string]any{"name": regionGroupName(c), "type": "url-test", "url": TestURL, "interval": 300, "tolerance": 50, "lazy": true, "proxies": byRegion[c]})
	}

	// Template groups: decode, expand tokens, append after the generated ones.
	var tplGroups []map[string]any
	if g := mapGet(root, "proxy-groups"); g != nil {
		if err := g.Decode(&tplGroups); err != nil {
			return nil, fmt.Errorf("模板 proxy-groups 格式错误：%w", err)
		}
	}
	for _, g := range tplGroups {
		var list []string
		if raw, ok := g["proxies"].([]any); ok {
			for _, x := range raw {
				list = append(list, fmt.Sprint(x))
			}
		}
		g["proxies"] = expand(list, all, regionNames)
		gen = append(gen, g)
	}
	if err := mapSet(root, "proxies", proxies); err != nil {
		return nil, err
	}
	if err := mapSet(root, "proxy-groups", gen); err != nil {
		return nil, err
	}
	return yaml.Marshal(&doc)
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, v any) error {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return err
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = &n
			return nil
		}
	}
	// Insert proxies / groups before "rules" so the file reads naturally.
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "rules" {
			m.Content = append(m.Content[:i], append([]*yaml.Node{k, &n}, m.Content[i:]...)...)
			return nil
		}
	}
	m.Content = append(m.Content, k, &n)
	return nil
}

// ---------- sing-box ----------

func singboxOutbound(n Node) map[string]any {
	return map[string]any{
		"type": "vless", "tag": n.Name, "server": n.Server, "server_port": n.Port, "uuid": n.UUID,
		"flow": "xtls-rprx-vision", "packet_encoding": "xudp",
		"tls": map[string]any{
			"enabled": true, "server_name": n.SNI,
			"utls":    map[string]any{"enabled": true, "fingerprint": n.FP},
			"reality": map[string]any{"enabled": true, "public_key": n.PublicKey, "short_id": n.ShortID},
		},
	}
}

// SingBox renders a sing-box config. Template outbounds whose "outbounds"
// contain {{all}} / {{regions}} / {{groups}} are expanded; generated groups
// and nodes are inserted before them.
func SingBox(nodes []Node, tpl string) ([]byte, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultSingBox
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tpl), &cfg); err != nil {
		return nil, fmt.Errorf("模板不是合法 JSON：%w", err)
	}
	all := make([]string, len(nodes))
	for i, n := range nodes {
		all[i] = n.Name
	}
	codes, byRegion := regions(nodes)
	var regionNames []string
	for _, c := range codes {
		regionNames = append(regionNames, regionGroupName(c))
	}
	if len(all) == 0 {
		all = []string{"direct"}
	}
	outs := []any{
		map[string]any{"type": "selector", "tag": GroupSelect, "outbounds": append(append([]string{GroupAuto}, regionNames...), all...), "default": GroupAuto},
		map[string]any{"type": "urltest", "tag": GroupAuto, "outbounds": all, "url": TestURL, "interval": "5m", "tolerance": 50},
	}
	for _, c := range codes {
		outs = append(outs, map[string]any{"type": "urltest", "tag": regionGroupName(c), "outbounds": byRegion[c], "url": TestURL, "interval": "5m"})
	}
	if tplOuts, ok := cfg["outbounds"].([]any); ok {
		for _, o := range tplOuts {
			m, ok := o.(map[string]any)
			if !ok {
				continue
			}
			if raw, ok := m["outbounds"].([]any); ok {
				var list []string
				for _, x := range raw {
					list = append(list, fmt.Sprint(x))
				}
				exp := expand(list, all, regionNames)
				// sing-box has no fallback group; drop the name if present.
				var keep []string
				for _, x := range exp {
					if x != GroupFallback {
						keep = append(keep, x)
					}
				}
				m["outbounds"] = keep
			}
			outs = append(outs, m)
		}
	}
	for _, n := range nodes {
		outs = append(outs, singboxOutbound(n))
	}
	cfg["outbounds"] = outs
	return json.MarshalIndent(cfg, "", "  ")
}

// Detect picks the format from the client's User-Agent.
func Detect(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "sing-box") || strings.Contains(l, "sfa") || strings.Contains(l, "sfi") || strings.Contains(l, "sfm") || strings.Contains(l, "hiddify"):
		return "singbox"
	case strings.Contains(l, "clash") || strings.Contains(l, "mihomo") || strings.Contains(l, "stash"):
		return "mihomo"
	}
	return "base64"
}

// Userinfo formats the Subscription-Userinfo header.
func Userinfo(up, down, total uint64, expire int64) string {
	s := "upload=" + strconv.FormatUint(up, 10) + "; download=" + strconv.FormatUint(down, 10) + "; total=" + strconv.FormatUint(total, 10)
	if expire > 0 {
		s += "; expire=" + strconv.FormatInt(expire, 10)
	}
	return s
}
