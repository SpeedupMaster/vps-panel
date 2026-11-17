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
	for _, a := range allowed {
		ip := net.ParseIP(strings.TrimSpace(a))
		_, cidr, cerr := net.ParseCIDR(strings.TrimSpace(a))
		switch {
		case ip != nil && ip.To4() != nil:
			v4 = append(v4, ip.String())
		case ip != nil:
			v6 = append(v6, ip.String())
		case cerr == nil && cidr.IP.To4() != nil:
			v4 = append(v4, cidr.String())
		case cerr == nil:
			v6 = append(v6, cidr.String())
		}
	}
	var b strings.Builder
	// "add" then "delete" makes the delete safe when the table doesn't exist.
	fmt.Fprintf(&b, "add table inet %s\ndelete table inet %s\n", table, table)
	if port <= 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "table inet %s {\n", table)
	fmt.Fprintf(&b, "  set allow4 { type ipv4_addr; flags interval;%s }\n", elems(v4))
	fmt.Fprintf(&b, "  set allow6 { type ipv6_addr; flags interval;%s }\n", elems(v6))
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
