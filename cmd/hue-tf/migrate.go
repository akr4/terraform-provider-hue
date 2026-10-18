package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/migrate"
)

const migrateUsage = "usage: hue-tf migrate [DIR ...] [--write]"

// migrateCommand rewrites configurations for provider 0.1 into the forms that
// provider 0.2.0 takes. It reads the .tf files in each directory, without
// subdirectories, and prints the changes as a diff. With --write, it writes
// them. It changes no file when one cannot be read.
func migrateCommand(args []string, out io.Writer) error {
	write := false
	var dirs []string
	for _, arg := range args {
		switch {
		case arg == "--write":
			write = true
		case strings.HasPrefix(arg, "-"):
			return errors.New(migrateUsage)
		default:
			dirs = append(dirs, arg)
		}
	}
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	given := map[string]bool{}
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		given[abs] = true
	}
	type change struct {
		path          string
		before, after []byte
		mode          fs.FileMode
	}
	var changes []change
	var left, check, other []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !(strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json")) {
				continue
			}
			path := filepath.Join(dir, name)
			// Paths are shown from the working directory where they are under it.
			if abs, err := filepath.Abs(path); err == nil {
				if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
					path = rel
				} else {
					path = abs
				}
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.HasSuffix(name, ".tf.json") {
				if bytes.Contains(src, []byte("hue_scene")) {
					other = append(other, path+": migrate does not read the JSON syntax. Rewrite its hue_scene resources by hand.")
				}
				continue
			}
			r, err := migrate.File(path, src)
			if err != nil {
				return fmt.Errorf("%w\nNo files were changed", err)
			}
			for _, n := range r.Notes {
				if n.Left {
					left = append(left, n.String())
				} else {
					check = append(check, n.String())
				}
			}
			for _, m := range r.Modules {
				dir := filepath.Join(dir, m.Source)
				abs, err := filepath.Abs(dir)
				if err != nil || given[abs] || !hasScene(dir) {
					continue
				}
				other = append(other, fmt.Sprintf("%s:%d: module.%s uses %s, which has hue_scene resources. Run hue-tf migrate %s as well.", path, m.Range.Start.Line, m.Name, m.Source, dir))
			}
			if !bytes.Equal(src, r.Output) {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				changes = append(changes, change{path, src, r.Output, info.Mode().Perm()})
			}
		}
	}
	for _, c := range changes {
		fmt.Fprint(out, migrate.Diff(c.path, c.before, c.after))
	}
	for _, section := range []struct {
		title string
		lines []string
	}{
		{"Not rewritten. Rewrite these by hand:", left},
		{"Rewritten. Check these:", check},
		{"Also:", other},
	} {
		if len(section.lines) == 0 {
			continue
		}
		fmt.Fprintln(out, "\n"+section.title)
		for _, l := range section.lines {
			fmt.Fprintln(out, "  "+l)
		}
	}
	files := fmt.Sprintf("%d files", len(changes))
	if len(changes) == 1 {
		files = "1 file"
	}
	switch {
	case len(changes) == 0:
		fmt.Fprintln(out, "\nNothing to rewrite.")
	case !write:
		fmt.Fprintf(out, "\nPreview only. Run hue-tf migrate --write to rewrite %s. Neither the bridge nor the state is changed.\n", files)
	default:
		for i, c := range changes {
			if err := writeFile(c.path, c.after, c.mode); err != nil {
				return fmt.Errorf("%w\n%d of %s were rewritten", err, i, files)
			}
		}
		fmt.Fprintf(out, "\nRewrote %s. Run terraform plan: the rewritten scenes should show no changes.\n", files)
	}
	return nil
}

// hasScene reports whether a directory has a .tf file with a hue_scene.
func hasScene(dir string) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	for _, f := range files {
		if src, err := os.ReadFile(f); err == nil && bytes.Contains(src, []byte("hue_scene")) {
			return true
		}
	}
	return false
}

// writeFile replaces a file in one step, so that it is never left half
// written.
func writeFile(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
