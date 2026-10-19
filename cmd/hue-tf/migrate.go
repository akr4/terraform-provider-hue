package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/migrate"
	"github.com/akr4/terraform-provider-hue/internal/pull"
)

const migrateUsage = "usage: hue-tf migrate [DIR ...] [--write]"

// migrateCommand rewrites configurations for provider 0.1 into the forms that
// provider 0.2.0 takes. It reads the .tf files in each directory, without
// subdirectories, and in the local modules that the directories call from
// inside them, and prints the changes as a diff. With --write, it writes them.
// It changes no file when one cannot be parsed.
func migrateCommand(args []string, out io.Writer) error {
	write := false
	var named []string
	for _, arg := range args {
		switch {
		case arg == "--write":
			write = true
		case strings.HasPrefix(arg, "-"):
			return errors.New(migrateUsage)
		default:
			named = append(named, arg)
		}
	}
	if len(named) == 0 {
		named = []string{"."}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// resolve gives the absolute path of a directory without symbolic links,
	// so that two paths to one directory compare equal.
	resolve := func(dir string) string {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return dir
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			return real
		}
		return abs
	}
	// show names a path from the working directory where it is under it, also
	// when the path has no symbolic links and the working directory does.
	bases := []string{cwd}
	if real := resolve(cwd); real != cwd {
		bases = append(bases, real)
	}
	show := func(path string) string {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		for _, base := range bases {
			if rel, err := filepath.Rel(base, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return rel
			}
		}
		return abs
	}
	// A directory named twice is read once. The local modules inside the named
	// directories are read after them. A module outside them may be shared
	// with configurations that still take 0.1, so it is only pointed at.
	var read, roots []string
	seen := map[string]bool{}
	for _, dir := range named {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		if !seen[abs] {
			seen[abs] = true
			roots = append(roots, abs)
			read = append(read, dir)
		}
	}
	inside := func(abs string) bool {
		for _, root := range roots {
			if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	type change struct {
		path          string
		before, after []byte
	}
	type link struct{ path, target string }
	var changes []change
	var links []link
	var left, check, other []string
	// reading holds the directories that migrate reads, by resolve.
	reading := map[string]bool{}
	modules := 0
	for i := 0; i < len(read); i++ {
		dir := read[i]
		entries, err := os.ReadDir(dir)
		if err != nil {
			if i < len(roots) {
				return err
			}
			other = append(other, fmt.Sprintf("%s is a local module that migrate could not read: %v", show(dir), err))
			continue
		}
		reading[resolve(dir)] = true
		if i >= len(roots) {
			modules++
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !pull.IsConfigFile(name) {
				continue
			}
			path := show(filepath.Join(dir, name))
			if entry.Type()&fs.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(path)
				if err != nil {
					other = append(other, fmt.Sprintf("%s is a symbolic link that migrate could not follow: %v", path, err))
					continue
				}
				links = append(links, link{path, target})
				continue
			}
			if !entry.Type().IsRegular() {
				left = append(left, path+" is not a regular file, which migrate does not rewrite.")
				continue
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
				if err != nil || seen[abs] {
					continue
				}
				seen[abs] = true
				if inside(abs) {
					read = append(read, dir)
				} else if hasScene(dir) {
					other = append(other, fmt.Sprintf("%s:%d: module.%s uses %s, which is outside the directories that migrate reads and has hue_scene resources. Run hue-tf migrate %s as well, if no configuration on provider 0.1 shares it.", path, m.Range.Start.Line, m.Name, m.Source, shellWord(show(dir))))
				}
			}
			if !bytes.Equal(src, r.Output) {
				changes = append(changes, change{path, src, r.Output})
			}
		}
	}
	// A link is told about once every directory is known, since it may point
	// into a module that a later file calls.
	for _, l := range links {
		if reading[resolve(filepath.Dir(l.target))] {
			check = append(check, fmt.Sprintf("%s is a symbolic link to %s, which migrate reads instead.", l.path, show(l.target)))
		} else {
			left = append(left, fmt.Sprintf("%s is a symbolic link, which migrate does not rewrite. Run hue-tf migrate %s, where it points.", l.path, shellWord(show(filepath.Dir(l.target)))))
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
	if modules > 0 {
		noun := "modules"
		if modules == 1 {
			noun = "module"
		}
		fmt.Fprintf(out, "\nRead %d local %s with the directories.\n", modules, noun)
	}
	switch {
	case len(changes) == 0:
		fmt.Fprintln(out, "\nNothing to rewrite.")
	case !write:
		command := "hue-tf migrate --write"
		if dirs := read[:len(roots)]; !(len(dirs) == 1 && dirs[0] == ".") {
			for _, dir := range dirs {
				command += " " + shellWord(dir)
			}
		}
		fmt.Fprintf(out, "\nPreview only. Run %s to rewrite %s. Neither the bridge nor the state is changed.\n", command, files)
	default:
		for i, c := range changes {
			if err := pull.ReplaceFile(c.path, c.before, c.after, ".hue-tf-migrate-*"); err != nil {
				return fmt.Errorf("%s: %w\n%d of %s were rewritten", c.path, err, i, files)
			}
		}
		fmt.Fprintf(out, "\nRewrote %s. Run terraform plan: the rewritten scenes should show no changes.\n", files)
	}
	return nil
}

// hasScene reports whether a directory has configuration with a hue_scene.
func hasScene(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !pull.IsConfigFile(name) {
			continue
		}
		if src, err := os.ReadFile(filepath.Join(dir, name)); err == nil && bytes.Contains(src, []byte("hue_scene")) {
			return true
		}
	}
	return false
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// shellWord quotes a path for a command that the user may copy, when the
// path needs it.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return shellQuote(s)
}
