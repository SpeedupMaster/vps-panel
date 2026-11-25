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

	"vpspanel/internal/common/xrayconf"
)

type Node struct {
	Name        string
	Server      string
	Port        int
	UUID        string
	SNI         string
	PublicKey   string
	ShortID     string
	FP          string
	RegionCode  string // landing country for chains, entry country for direct
	XrayVersion string // entry server's Xray version; empty = latest
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

// countryNames maps ISO 3166-1 alpha-2 codes to Chinese display names.
var countryNames = map[string]string{
	"HK": "香港", "TW": "台湾", "MO": "澳门", "CN": "中国",
	"JP": "日本", "SG": "新加坡", "KR": "韩国", "US": "美国",
	"GB": "英国", "UK": "英国", "DE": "德国", "FR": "法国",
	"NL": "荷兰", "CA": "加拿大", "AU": "澳大利亚", "NZ": "新西兰",
	"RU": "俄罗斯", "IN": "印度", "MY": "马来西亚", "TH": "泰国",
	"VN": "越南", "PH": "菲律宾", "ID": "印度尼西亚", "TR": "土耳其",
	"IT": "意大利", "ES": "西班牙", "SE": "瑞典", "CH": "瑞士",
	"FI": "芬兰", "NO": "挪威", "PL": "波兰", "IE": "爱尔兰",
	"AT": "奥地利", "BE": "比利时", "LU": "卢森堡", "CZ": "捷克",
	"BR": "巴西", "AR": "阿根廷", "CL": "智利", "MX": "墨西哥",
	"ZA": "南非", "AE": "阿联酋", "IL": "以色列", "UA": "乌克兰",
	"OT": "其他地区",
}

// aiExcludedRegions lists regions commonly restricted by major overseas AI
// providers (OpenAI / Anthropic / Google Gemini), excluded from {{regions_ai}}.
var aiExcludedRegions = map[string]bool{
	"CN": true,
	"HK": true,
	"MO": true,
	"RU": true,
}

func isAIExcludedRegion(cc string) bool {
	return aiExcludedRegions[strings.ToUpper(strings.TrimSpace(cc))]
}

// regions groups node names by region code, sorted by code with "OT" last.
func regions(nodes []Node) ([]string, map[string][]string) {
	m := map[string][]string{}
	for _, n := range nodes {
		cc := strings.ToUpper(strings.TrimSpace(n.RegionCode))
		if cc == "" {
			cc = "OT"
		}
		m[cc] = append(m[cc], n.Name)
	}
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		if codes[i] == "OT" {
			return false
		}
		if codes[j] == "OT" {
			return true
		}
		return codes[i] < codes[j]
	})
	return codes, m
}

func regionGroupName(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if cc == "" || cc == "OT" {
		return "🏳 " + countryNames["OT"]
	}
	name := countryNames[cc]
	if name == "" {
		name = cc
	}
	return Flag(cc) + " " + name
}

// RegionGroupName returns the generated proxy-group name for an ISO country code.
func RegionGroupName(cc string) string { return regionGroupName(cc) }

// ReservedNames returns all built-in and default-template proxy-group names
// (including all country region groups) that node names must not collide with.
func ReservedNames() map[string]bool {
	res := map[string]bool{
		GroupSelect:   true,
		GroupAuto:     true,
		GroupFallback: true,
		"DIRECT":      true,
		"REJECT":      true,
		"direct":      true,
		"reject":      true,
		"Apple 推送":    true,
		"AI":          true,
		"Claude":      true,
		"流媒体":         true,
		"🤖 AI 服务":     true,
		"🧠 Claude":    true,
		"📺 YouTube":   true,
		"🎬 国际流媒体":     true,
		"💬 社交通讯":      true,
		"🔍 Google":    true,
		"💻 开发与学术":     true,
		"🪟 微软服务":      true,
		"🔔 苹果推送":      true,
		"🍎 Apple 服务":  true,
		"🎮 游戏平台":      true,
		"💰 金融与加密":     true,
		"📺 国内媒体":      true,
		"🛡️ 广告拦截":     true,
		"🌐 漏网之鱼":      true,
	}
	for cc := range countryNames {
		res[regionGroupName(cc)] = true
	}
	return res
}

// DisambiguateNodes returns a copy of nodes whose names are unique and do not
// collide with any name in reserved.
func DisambiguateNodes(nodes []Node, reserved map[string]bool) []Node {
	seen := make(map[string]int, len(nodes)+len(reserved))
	for name := range reserved {
		seen[name] = 1
	}
	for _, n := range nodes {
		seen[regionGroupName(n.RegionCode)] = 1
	}
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		base := strings.TrimSpace(n.Name)
		if base == "" {
			base = "node"
		}
		cand := base
		for k := 1; seen[cand] > 0; {
			k++
			cand = fmt.Sprintf("%s %d", base, k)
		}
		n.Name = cand
		seen[cand]++
		out[i] = n
	}
	return out
}

// expand replaces the {{all}} / {{regions}} / {{regions_ai}} / {{groups}}
// tokens in a proxy list, deduplicating while preserving order.
func expand(list []string, all, regionNames, aiRegionNames []string) []string {
	var out []string
	seen := make(map[string]bool, len(list))
	add := func(items ...string) {
		for _, item := range items {
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
	}
	for _, x := range list {
		switch x {
		case "{{all}}":
			add(all...)
		case "{{regions}}":
			add(regionNames...)
		case "{{regions_ai}}":
			add(aiRegionNames...)
		case "{{groups}}":
			add(GroupSelect, GroupAuto, GroupFallback)
			add(regionNames...)
		default:
			add(x)
		}
	}
	return out
}

// ---------- mihomo ----------

func mihomoProxy(n Node) map[string]any {
	realityOpts := map[string]any{"public-key": n.PublicKey, "short-id": n.ShortID}
	if xrayconf.SupportsX25519MLKEM768(n.XrayVersion) {
		realityOpts["support-x25519mlkem768"] = true
	}
	return map[string]any{
		"name": n.Name, "type": "vless", "server": n.Server, "port": n.Port, "uuid": n.UUID,
		"network": "tcp", "tls": true, "udp": true, "flow": "xtls-rprx-vision",
		"servername": n.SNI, "client-fingerprint": n.FP,
		"reality-opts": realityOpts,
	}
}

// Mihomo renders a Clash Meta config. The template supplies everything but
// proxies and the generated groups; its own groups may use the
// {{all}} / {{regions}} / {{regions_ai}} / {{groups}} tokens inside `proxies`.
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

	// Template groups: decode first so their names are reserved before node names are emitted.
	var tplGroups []map[string]any
	if g := mapGet(root, "proxy-groups"); g != nil {
		if err := g.Decode(&tplGroups); err != nil {
			return nil, fmt.Errorf("模板 proxy-groups 格式错误：%w", err)
		}
	}
	reserved := ReservedNames()
	for _, g := range tplGroups {
		if name, ok := g["name"].(string); ok && name != "" {
			reserved[name] = true
		}
	}
	nodes = DisambiguateNodes(nodes, reserved)

	all := make([]string, len(nodes))
	proxies := make([]any, len(nodes))
	for i, n := range nodes {
		all[i] = n.Name
		proxies[i] = mihomoProxy(n)
	}
	codes, byRegion := regions(nodes)
	var regionNames, aiRegionNames []string
	for _, c := range codes {
		name := regionGroupName(c)
		regionNames = append(regionNames, name)
		if !isAIExcludedRegion(c) {
			aiRegionNames = append(aiRegionNames, name)
		}
	}
	if len(all) == 0 {
		all = []string{"DIRECT"}
	}
	gen := []any{
		map[string]any{"name": GroupSelect, "type": "select", "proxies": expand(append(append([]string{GroupAuto, GroupFallback}, regionNames...), append(all, "DIRECT")...), nil, nil, nil)},
		map[string]any{"name": GroupAuto, "type": "url-test", "url": TestURL, "interval": 300, "tolerance": 50, "lazy": true, "proxies": all},
		map[string]any{"name": GroupFallback, "type": "fallback", "url": TestURL, "interval": 300, "lazy": true, "proxies": all},
	}
	for _, c := range codes {
		gen = append(gen, map[string]any{"name": regionGroupName(c), "type": "url-test", "url": TestURL, "interval": 300, "tolerance": 50, "lazy": true, "proxies": byRegion[c]})
	}

	for _, g := range tplGroups {
		var list []string
		if raw, ok := g["proxies"].([]any); ok {
			for _, x := range raw {
				list = append(list, fmt.Sprint(x))
			}
		}
		g["proxies"] = expand(list, all, regionNames, aiRegionNames)
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
// contain {{all}} / {{regions}} / {{regions_ai}} / {{groups}} are expanded;
// generated groups and nodes are inserted before them.
func SingBox(nodes []Node, tpl string) ([]byte, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultSingBox
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tpl), &cfg); err != nil {
		return nil, fmt.Errorf("模板不是合法 JSON：%w", err)
	}
	reserved := ReservedNames()
	if tplOuts, ok := cfg["outbounds"].([]any); ok {
		for _, o := range tplOuts {
			if m, ok := o.(map[string]any); ok {
				if tag, ok := m["tag"].(string); ok && tag != "" {
					reserved[tag] = true
				}
			}
		}
	}
	nodes = DisambiguateNodes(nodes, reserved)

	all := make([]string, len(nodes))
	for i, n := range nodes {
		all[i] = n.Name
	}
	codes, byRegion := regions(nodes)
	var regionNames, aiRegionNames []string
	for _, c := range codes {
		name := regionGroupName(c)
		regionNames = append(regionNames, name)
		if !isAIExcludedRegion(c) {
			aiRegionNames = append(aiRegionNames, name)
		}
	}
	if len(all) == 0 {
		all = []string{"direct"}
	}
	outs := []any{
		map[string]any{"type": "selector", "tag": GroupSelect, "outbounds": expand(append(append([]string{GroupAuto}, regionNames...), all...), nil, nil, nil), "default": GroupAuto},
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
				exp := expand(list, all, regionNames, aiRegionNames)
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
