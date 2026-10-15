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

// Write keeps a private backup and refuses stale edits or symlink destinations.
func (c *Change) Write() (string, error) {
	if len(c.Edits) == 0 {
		return "", nil
	}
	info, err := os.Lstat(c.Path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination must be a regular file")
	}
	current, err := os.ReadFile(c.Path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, c.Original) {
		return "", fmt.Errorf("file changed since preview")
	}
	backup, err := os.CreateTemp(filepath.Dir(c.Path), ".hue-pull-backup-*")
	if err != nil {
		return "", err
	}
	backupName := backup.Name()
	_, err = backup.Write(c.Original)
	closeErr := backup.Close()
	if err != nil {
		return backupName, err
	}
	if closeErr != nil {
		return backupName, closeErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), ".hue-pull-*")
	if err != nil {
		return backupName, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(c.Updated); err != nil {
		tmp.Close()
		return backupName, err
	}
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return backupName, err
	}
	if err = tmp.Close(); err != nil {
		return backupName, err
	}
	return backupName, os.Rename(tmp.Name(), c.Path)
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
