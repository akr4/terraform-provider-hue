package migrate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Diff returns a unified diff from before to after with three lines of
// context, as diff -u prints it, or "" when they are the same. A relative name
// gets the a/ and b/ prefixes that git uses.
func Diff(name string, before, after []byte) string {
	ops := diffLines(lines(before), lines(after))
	const context = 3
	// The line numbers before each op, from 0.
	at := make([][2]int, len(ops)+1)
	for i, o := range ops {
		at[i+1] = at[i]
		if o.kind != '+' {
			at[i+1][0]++
		}
		if o.kind != '-' {
			at[i+1][1]++
		}
	}
	var b strings.Builder
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start, end := max(0, i-context), i
		for {
			for end < len(ops) && ops[end].kind != ' ' {
				end++
			}
			next := end
			for next < len(ops) && ops[next].kind == ' ' {
				next++
			}
			// Changes close together share a hunk.
			if next < len(ops) && next-end <= 2*context {
				end = next
				continue
			}
			end = min(len(ops), end+context)
			break
		}
		if b.Len() == 0 {
			if filepath.IsAbs(name) {
				fmt.Fprintf(&b, "--- %s\n+++ %s\n", name, name)
			} else {
				fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
			}
		}
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(at[start][0], at[end][0]-at[start][0]), hunkRange(at[start][1], at[end][1]-at[start][1]))
		for _, o := range ops[start:end] {
			b.WriteByte(o.kind)
			b.WriteString(o.text)
			b.WriteByte('\n')
		}
		i = end
	}
	return b.String()
}

func hunkRange(start, n int) string {
	switch n {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

func lines(data []byte) []string {
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// diffLines returns the longest common subsequence of a and b as ops, with
// removed lines before added ones.
func diffLines(a, b []string) []op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	am, bm := a[p:len(a)-s], b[p:len(b)-s]
	ops := make([]op, 0, len(a)+len(b))
	for _, l := range a[:p] {
		ops = append(ops, op{' ', l})
	}
	n, m := len(am), len(bm)
	// Past this size, the changed lines are listed whole rather than matched.
	if n*m > 1<<24 {
		for _, l := range am {
			ops = append(ops, op{'-', l})
		}
		for _, l := range bm {
			ops = append(ops, op{'+', l})
		}
	} else {
		// lcs[i][j] is the length of the common subsequence of am[i:] and bm[j:].
		lcs := make([][]int32, n+1)
		for i := range lcs {
			lcs[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if am[i] == bm[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		for i, j := 0, 0; i < n || j < m; {
			switch {
			case i < n && j < m && am[i] == bm[j]:
				ops = append(ops, op{' ', am[i]})
				i++
				j++
			case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
				ops = append(ops, op{'-', am[i]})
				i++
			default:
				ops = append(ops, op{'+', bm[j]})
				j++
			}
		}
	}
	for _, l := range a[len(a)-s:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}
