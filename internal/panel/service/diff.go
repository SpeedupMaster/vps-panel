package service

import (
	"fmt"
	"strings"
)

// LineDiff returns a compact unified-style diff (3 lines of context).
// Configs are a few hundred lines, so the O(n·m) LCS is fine.
func LineDiff(a, b string) string {
	if a == b {
		return ""
	}
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	if a == "" {
		x = nil
	}
	n, m := len(x), len(y)
	if n*m > 4_000_000 {
		return "(配置过大，省略差异)"
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int
		bj   int
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i, j})
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', y[j], i, j})
			j++
		default:
			ops = append(ops, op{'-', x[i], i, j})
			i++
		}
	}
	// Emit hunks with context.
	const ctx = 3
	var out strings.Builder
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-ctx, 0)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// stop when the next change is more than 2*ctx lines away
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*ctx {
				end = min(end+ctx, len(ops))
				break
			}
			end = run
		}
		fmt.Fprintf(&out, "@@ -%d +%d @@\n", ops[start].ai+1, ops[start].bj+1)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String()
}
