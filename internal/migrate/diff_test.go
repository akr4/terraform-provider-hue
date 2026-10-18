package migrate

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\n"
	after := "a\nb\nc\nD\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n"
	want := `--- a/main.tf
+++ b/main.tf
@@ -1,7 +1,7 @@
 a
 b
 c
-d
+D
 e
 f
 g
@@ -11,3 +11,4 @@
 k
 l
 m
+n
`
	if got := Diff("main.tf", []byte(before), []byte(after)); got != want {
		t.Errorf("got\n%s", got)
	}
	if got := Diff("main.tf", []byte(before), []byte(before)); got != "" {
		t.Errorf("diff of the same file: %s", got)
	}
	// Changes three lines apart or closer share a hunk.
	if got := Diff("x", []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), []byte("1\nX\n3\n4\n5\n6\n7\nY\n")); strings.Count(got, "@@ -") != 1 {
		t.Errorf("got\n%s", got)
	}
	if got := Diff("x", nil, []byte("a\n")); got != "--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+a\n" {
		t.Errorf("got\n%s", got)
	}
}

// patch applies a diff that Diff printed.
func patch(t *testing.T, before []string, diff string) []string {
	t.Helper()
	var out []string
	at := 0
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	for i := 2; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "@@ -") {
			var start int
			fmt.Sscanf(strings.TrimPrefix(l, "@@ -"), "%d", &start)
			// A hunk that removes nothing names the line before it.
			if strings.HasPrefix(strings.Fields(l)[1], "-"+strconv.Itoa(start)+",0") {
				start++
			}
			for at < start-1 {
				out = append(out, before[at])
				at++
			}
			continue
		}
		switch l[0] {
		case ' ':
			if before[at] != l[1:] {
				t.Fatalf("context %q at line %d is %q", l[1:], at+1, before[at])
			}
			out = append(out, l[1:])
			at++
		case '-':
			if before[at] != l[1:] {
				t.Fatalf("removed %q at line %d is %q", l[1:], at+1, before[at])
			}
			at++
		case '+':
			out = append(out, l[1:])
		}
	}
	return append(out, before[at:]...)
}

func TestDiffApplies(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	words := []string{"a", "b", "c", "d", "}", "{", ""}
	for n := 0; n < 500; n++ {
		var before []string
		for i := random.Intn(40); i > 0; i-- {
			before = append(before, words[random.Intn(len(words))])
		}
		after := append([]string(nil), before...)
		for i := random.Intn(6); i > 0; i-- {
			at := random.Intn(len(after) + 1)
			switch random.Intn(3) {
			case 0:
				after = append(after[:at], append([]string{"new"}, after[at:]...)...)
			case 1:
				if at < len(after) {
					after = append(after[:at], after[at+1:]...)
				}
			case 2:
				if at < len(after) {
					after[at] = "changed"
				}
			}
		}
		join := func(l []string) []byte {
			if len(l) == 0 {
				return nil
			}
			return []byte(strings.Join(l, "\n") + "\n")
		}
		diff := Diff("x", join(before), join(after))
		if diff == "" {
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatalf("no diff from %q to %q", before, after)
			}
			continue
		}
		if got := patch(t, before, diff); strings.Join(got, "\n") != strings.Join(after, "\n") {
			t.Fatalf("diff from %q to %q:\n%s\napplies as %q", before, after, diff, got)
		}
	}
}
