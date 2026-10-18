package migrate

import (
	"fmt"
	"path/filepath"
	"sort"
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
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// diffLines returns ops that turn a into b, with removed lines before added
// ones. Lines that appear once in each, such as the names of scenes, split
// long files into short parts first, as patience diff does, and each part is
// matched by its longest common subsequence. A file with many changes then
// needs little memory.
func diffLines(a, b []string) []op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	ops := make([]op, 0, len(a)+len(b))
	for _, l := range a[:p] {
		ops = append(ops, op{' ', l})
	}
	am, bm := a[p:len(a)-s], b[p:len(b)-s]
	var anchors []anchor
	if len(am)*len(bm) > 1<<16 {
		anchors = uniqueCommon(am, bm)
	}
	if len(anchors) > 0 {
		i, j := 0, 0
		for _, an := range anchors {
			ops = append(ops, diffLines(am[i:an.i], bm[j:an.j])...)
			ops = append(ops, op{' ', am[an.i]})
			i, j = an.i+1, an.j+1
		}
		ops = append(ops, diffLines(am[i:], bm[j:])...)
	} else {
		ops = append(ops, common(am, bm)...)
	}
	for _, l := range a[len(a)-s:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

type anchor struct{ i, j int }

// uniqueCommon returns the lines that appear once in a and once in b, at
// their positions, where they keep their order in both.
func uniqueCommon(a, b []string) []anchor {
	type seen struct{ inA, inB, j int }
	lines := map[string]*seen{}
	for _, l := range a {
		if lines[l] == nil {
			lines[l] = &seen{}
		}
		lines[l].inA++
	}
	for j, l := range b {
		if x := lines[l]; x != nil {
			x.inB++
			x.j = j
		}
	}
	var pairs []anchor
	for i, l := range a {
		if x := lines[l]; x.inA == 1 && x.inB == 1 {
			pairs = append(pairs, anchor{i, x.j})
		}
	}
	// The longest run of pairs whose j increase, by patience sorting.
	var tails []int
	prev := make([]int, len(pairs))
	for x, p := range pairs {
		k := sort.Search(len(tails), func(k int) bool { return pairs[tails[k]].j >= p.j })
		prev[x] = -1
		if k > 0 {
			prev[x] = tails[k-1]
		}
		if k == len(tails) {
			tails = append(tails, x)
		} else {
			tails[k] = x
		}
	}
	if len(tails) == 0 {
		return nil
	}
	out := make([]anchor, len(tails))
	x := tails[len(tails)-1]
	for k := len(tails) - 1; k >= 0; k-- {
		out[k] = pairs[x]
		x = prev[x]
	}
	return out
}

// common returns ops for the longest common subsequence of a and b. Past a
// size, the lines are listed as removed and added.
func common(a, b []string) []op {
	n, m := len(a), len(b)
	var ops []op
	if n*m > 1<<22 {
		for _, l := range a {
			ops = append(ops, op{'-', l})
		}
		for _, l := range b {
			ops = append(ops, op{'+', l})
		}
		return ops
	}
	// lcs[i][j] is the length of the common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	return ops
}
