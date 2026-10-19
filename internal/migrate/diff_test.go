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
		// Long files are split at their unique lines first.
		length := random.Intn(40)
		if n%10 == 0 {
			length = 300 + random.Intn(600)
		}
		for i := length; i > 0; i-- {
			if random.Intn(5) == 0 {
				before = append(before, fmt.Sprintf("line %d", random.Intn(1000)))
				continue
			}
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

// Configuration that Terraform generated for 0.1 writes an empty palette in
// every scene, and migrate removes each. The diff has a hunk for each scene,
// and only the removed lines.
func TestDiffLarge(t *testing.T) {
	var before, after []string
	for i := 0; i < 2000; i++ {
		scene := []string{fmt.Sprintf(`resource "hue_scene" "s%d" {`, i), "  actions = {"}
		for light := 0; light < 6; light++ {
			scene = append(scene, "    (local.light) = { on = true }")
		}
		scene = append(scene, "  }")
		palette := []string{"  palette = jsonencode({", "    color = []", "  })"}
		before = append(append(append(before, scene...), palette...), "}")
		after = append(append(after, scene...), "}")
	}
	join := func(l []string) []byte { return []byte(strings.Join(l, "\n") + "\n") }
	diff := Diff("main.tf", join(before), join(after))
	if got := strings.Count(diff, "\n-"); got != 2000*3 {
		t.Errorf("%d lines removed", got)
	}
	if strings.Contains(diff, "\n+ ") || strings.Count(diff, "@@ -") != 2000 {
		t.Errorf("diff is not minimal: %d hunks", strings.Count(diff, "@@ -"))
	}
	if got := patch(t, before, diff); strings.Join(got, "\n") != strings.Join(after, "\n") {
		t.Fatal("the diff does not apply")
	}
}
