// Package pull reads Terraform state and configuration for hue-tf
// import-blocks, and writes the import blocks it prepares.
package pull

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Edit struct {
	Start, End    int
	Before, After string
	Field         string
}
type Change struct {
	Path              string
	Original, Updated []byte
	Edits             []Edit
}

func (c *Change) finish() {
	sort.Slice(c.Edits, func(i, j int) bool { return c.Edits[i].Start < c.Edits[j].Start })
	c.Updated = append([]byte(nil), c.Original...)
	for i := len(c.Edits) - 1; i >= 0; i-- {
		e := c.Edits[i]
		next := append([]byte(nil), c.Updated[:e.Start]...)
		next = append(next, e.After...)
		next = append(next, c.Updated[e.End:]...)
		c.Updated = next
	}
}

// Write replaces the file atomically and refuses stale edits or symlink
// destinations. The caller keeps the backup of the original: import-blocks
// saves it in its transaction directory before writing anything.
func (c *Change) Write() error {
	if len(c.Edits) == 0 {
		return nil
	}
	return ReplaceFile(c.Path, c.Original, c.Updated, ".hue-pull-*")
}

// ReplaceFile replaces a regular file with updated in one step, keeping its
// permissions. It refuses a symbolic link or another kind of file, and a file
// that no longer holds original, so that an edit saved after it was read is
// not lost. The temporary file is named by pattern, as os.CreateTemp takes it.
func ReplaceFile(path string, original, updated []byte, pattern string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("destination must be a regular file")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("file changed since preview")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(updated); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// WriteNew creates a file and never replaces an existing one.
func WriteNew(path string, src []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(src)
	closeErr := f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}
	if closeErr != nil {
		os.Remove(path)
		return closeErr
	}
	return nil
}
