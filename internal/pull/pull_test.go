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
