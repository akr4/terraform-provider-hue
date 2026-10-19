package pull

import (
	"os"
	"path/filepath"
	"testing"
)

func appendChange(t *testing.T, path, original, addition string) *Change {
	t.Helper()
	if err := os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	c := &Change{Path: path, Original: []byte(original), Edits: []Edit{{Start: len(original), End: len(original), After: addition, Field: "import blocks"}}}
	c.finish()
	return c
}

func TestChangeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "imports_hue.tf")
	c := appendChange(t, path, "# keep\n", "import {}\n")
	if err := c.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# keep\nimport {}\n" {
		t.Fatal(string(got))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("mode changed")
	}
	// The caller keeps the backup, so nothing else is left next to the file.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("left %d files", len(entries))
	}
}

func TestWriteNewRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imports_hue.tf")
	if err := WriteNew(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, []byte("replacement")); err == nil {
		t.Fatal("overwrote file")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Fatal(string(got))
	}
}

func TestChangeWriteRefusesStaleFileAndSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "imports_hue.tf")
		c := appendChange(t, path, "# keep\n", "import {}\n")
		if symlink {
			os.Rename(path, path+".original")
			os.Symlink(path+".original", path)
		} else {
			os.WriteFile(path, []byte("# keep\n# new edit\n"), 0600)
		}
		if err := c.Write(); err == nil {
			t.Fatal("overwrote stale file/symlink")
		}
	}
}

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	// An edit saved after the file was read is not lost.
	if err := ReplaceFile(path, []byte("older\n"), []byte("new\n"), ".test-*"); err == nil || err.Error() != "file changed since preview" {
		t.Fatalf("got %v", err)
	}
	if err := ReplaceFile(path, []byte("old\n"), []byte("new\n"), ".test-*"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, %v", info.Mode(), err)
	}
	// A symbolic link stays a link, and its target is not written either.
	link := filepath.Join(dir, "link.tf")
	if err := os.Symlink("main.tf", link); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(link, []byte("new\n"), []byte("newer\n"), ".test-*"); err == nil {
		t.Fatal("replaced a link")
	}
	if got, _ := os.ReadFile(path); string(got) != "new\n" {
		t.Fatalf("the target was written: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".test-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}
