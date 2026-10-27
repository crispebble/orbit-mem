package memory

import (
	"fmt"
	"strings"
)

// LineDiff is a unified-style line diff of a and b with ctx lines of context.
// It is meant for people to read in a proposal, not for patch tools.
func LineDiff(a, b string, ctx int) string {
	al, bl := splitLines(a), splitLines(b)
	n, m := len(al), len(bl)
	// LCS table; proposals are small files, so O(n*m) is fine. Very large inputs fall back to whole replacement.
	if n*m > 4_000_000 {
		var sb strings.Builder
		for _, l := range al {
			sb.WriteString("-" + l + "\n")
		}
		for _, l := range bl {
			sb.WriteString("+" + l + "\n")
		}
		return sb.String()
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	type op struct {
		c    byte
		line string
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			ops = append(ops, op{' ', al[i]})
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', bl[j]})
			j++
		default:
			ops = append(ops, op{'-', al[i]})
			i++
		}
	}
	keep := make([]bool, len(ops))
	for k, o := range ops {
		if o.c == ' ' {
			continue
		}
		for d := k - ctx; d <= k+ctx; d++ {
			if d >= 0 && d < len(ops) {
				keep[d] = true
			}
		}
	}
	var sb strings.Builder
	gap := false
	for k, o := range ops {
		if !keep[k] {
			gap = true
			continue
		}
		if gap && sb.Len() > 0 {
			sb.WriteString("@@\n")
		}
		gap = false
		fmt.Fprintf(&sb, "%c%s\n", o.c, o.line)
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
