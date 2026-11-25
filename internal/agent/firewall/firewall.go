// Package firewall restricts the landing SS port to the entry machines' IPs
// with a dedicated nftables table, so it never touches the user's own rules.
package firewall

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var StateFile = "/etc/vpsagent/firewall.nft"

const table = "vpspanel"

// Build renders the ruleset. An empty allow list removes the table.
func Build(port int, allowed []string) string {
	var v4, v6 []string
	seen4 := map[string]bool{}
	seen6 := map[string]bool{}
	add := func(list *[]string, seen map[string]bool, elem string) {
		if !seen[elem] {
			seen[elem] = true
			*list = append(*list, elem)
		}
	}
	for _, a := range allowed {
		s := strings.TrimSpace(a)
		if s == "" {
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				add(&v4, seen4, ip4.String())
			} else {
				add(&v6, seen6, ip.String())
			}
			continue
		}
		if _, cidr, err := net.ParseCIDR(s); err == nil {
			ones, bits := cidr.Mask.Size()
			elem := cidr.String()
			if ones == bits {
				elem = cidr.IP.String()
			}
			if cidr.IP.To4() != nil {
				add(&v4, seen4, elem)
			} else {
				add(&v6, seen6, elem)
			}
		}
	}
	var b strings.Builder
	// "add" then "delete" makes the delete safe when the table doesn't exist.
	fmt.Fprintf(&b, "add table inet %s\ndelete table inet %s\n", table, table)
	if port <= 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "table inet %s {\n", table)
	fmt.Fprintf(&b, "  set allow4 { type ipv4_addr; flags interval; auto-merge;%s }\n", elems(v4))
	fmt.Fprintf(&b, "  set allow6 { type ipv6_addr; flags interval; auto-merge;%s }\n", elems(v6))
	b.WriteString("  chain input {\n    type filter hook input priority -10; policy accept;\n")
	for _, proto := range []string{"tcp", "udp"} {
		fmt.Fprintf(&b, "    %s dport %d ip saddr @allow4 accept\n", proto, port)
		fmt.Fprintf(&b, "    %s dport %d ip6 saddr @allow6 accept\n", proto, port)
		fmt.Fprintf(&b, "    %s dport %d counter drop\n", proto, port)
	}
	b.WriteString("  }\n}\n")
	return b.String()
}

func elems(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return " elements = { " + strings.Join(list, ", ") + " };"
}

func load(rules string) error {
	if _, err := exec.LookPath("nft"); err != nil {
		return fmt.Errorf("未安装 nftables")
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apply loads the whitelist and persists it for Restore.
func Apply(port int, allowed []string) error {
	rules := Build(port, allowed)
	if err := load(rules); err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(StateFile), 0o700)
	return os.WriteFile(StateFile, []byte(rules), 0o600)
}

// Restore re-applies the persisted ruleset (called at agent start).
func Restore() {
	if b, err := os.ReadFile(StateFile); err == nil {
		_ = load(string(b))
	}
}
