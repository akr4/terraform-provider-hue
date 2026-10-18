package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const migrateBefore = `module "room" {
  source = "./modules/room"
}

resource "hue_scene" "evening" {
  name  = "Evening"
  group = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  actions = {
    "11111111-1111-4111-8111-111111111111" = { on = true, kelvin = 2700 }
  }
  palette = jsonencode({ color = [], color_temperature = [], dimming = [], effects = [], effects_v2 = [] })
}

resource "hue_scene" "party" {
  name    = "Party"
  group   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  actions = local.party
}
`

const migrateAfter = `module "room" {
  source = "./modules/room"
}

resource "hue_scene" "evening" {
  name  = "Evening"
  group = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  actions = {
    "11111111-1111-4111-8111-111111111111" = { on = true, mirek = provider::hue::mirek_from_kelvin(2700) }
  }
}

resource "hue_scene" "party" {
  name    = "Party"
  group   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  actions = local.party
}
`

func writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func runMigrate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runWith(context.Background(), append([]string{"migrate"}, args...), &out, &bytes.Buffer{}, dependencies{})
	return out.String(), err
}

func TestMigrate(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, map[string]string{
		"main.tf":                migrateBefore,
		"modules/room/scenes.tf": "resource \"hue_scene\" \"this\" {\n  actions = {}\n}\n",
		"generated.tf.json":      `{"resource":{"hue_scene":{"x":{}}}}`,
		"other.tf.json":          `{"resource":{"other":{"x":{}}}}`,
	})
	out, err := runMigrate(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--- a/main.tf\n+++ b/main.tf\n",
		"-    \"11111111-1111-4111-8111-111111111111\" = { on = true, kelvin = 2700 }\n",
		"+    \"11111111-1111-4111-8111-111111111111\" = { on = true, mirek = provider::hue::mirek_from_kelvin(2700) }\n",
		"Not rewritten. Rewrite these by hand:\n  main.tf:17: hue_scene.party: actions use local.party",
		"Rewritten. Check these:\n  main.tf:11: hue_scene.evening: palette had no entries",
		"Also:\n  generated.tf.json: migrate does not read the JSON syntax.",
		"Read 1 local module with the directories.",
		"Preview only. Run hue-tf migrate --write to rewrite 1 file. Neither the bridge nor the state is changed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "other.tf.json") {
		t.Errorf("listed a JSON file without hue_scene:\n%s", out)
	}
	if got, _ := os.ReadFile("main.tf"); string(got) != migrateBefore {
		t.Fatalf("the preview changed main.tf:\n%s", got)
	}

	if out, err = runMigrate(t, "--write"); err != nil || !strings.Contains(out, "Rewrote 1 file.") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile("main.tf"); string(got) != migrateAfter {
		t.Fatalf("main.tf was rewritten as\n%s", got)
	}
	if info, err := os.Stat("main.tf"); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("main.tf has mode %v, %v", info.Mode(), err)
	}
	if left, _ := filepath.Glob(".hue-tf-migrate-*"); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// The module named on the command line is migrated, and not pointed out.
	if out, err = runMigrate(t, ".", "modules/room"); err != nil || !strings.HasSuffix(out, "\nNothing to rewrite.\n") || strings.Contains(out, "module.room") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestMigrateChangesNothingOnError(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, map[string]string{
		"a.tf": migrateBefore,
		"b.tf": "resource \"hue_scene\" \"broken\" {\n",
	})
	out, err := runMigrate(t, "--write")
	if err == nil || !strings.Contains(err.Error(), "No files were changed") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile("a.tf"); string(got) != migrateBefore {
		t.Fatal("a.tf was changed")
	}
}

func TestMigrateUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runMigrate(t, "--force"); err == nil || err.Error() != migrateUsage {
		t.Fatalf("got %v", err)
	}
	if _, err := runMigrate(t, "missing"); err == nil {
		t.Fatal("read a missing directory")
	}
	if out, err := runMigrate(t); err != nil || out != "\nNothing to rewrite.\n" {
		t.Fatalf("%v\n%q", err, out)
	}
}

// A directory named twice, as . and ./, is read once, and the command that
// the preview suggests names the directories again.
func TestMigrateDirectories(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, map[string]string{"main.tf": migrateBefore, "my scenes/main.tf": migrateBefore, "..scenes/main.tf": migrateBefore})
	out, err := runMigrate(t, ".", "./")
	if err != nil || strings.Count(out, "--- a/main.tf") != 1 || !strings.Contains(out, "Run hue-tf migrate --write to rewrite 1 file.") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runMigrate(t, "my scenes", "..scenes")
	if err != nil || !strings.Contains(out, "Run hue-tf migrate --write 'my scenes' ..scenes to rewrite 2 files.") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A directory whose name starts with .. is under the working directory.
	if !strings.Contains(out, "--- a/..scenes/main.tf") {
		t.Fatalf("..scenes is shown by another path:\n%s", out)
	}
}

// A symbolic link is not replaced by a file. migrate points at where it
// points, and reads the file there when that directory is named too.
func TestMigrateSymlink(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, map[string]string{"shared/scenes.tf": migrateBefore, "root/other.tf": "\n"})
	if err := os.Symlink("../shared/scenes.tf", "root/scenes.tf"); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrate(t, "--write", "root")
	if err != nil || !strings.Contains(out, "root/scenes.tf is a symbolic link, which migrate does not rewrite. Run hue-tf migrate shared, where it points.") || !strings.HasSuffix(out, "Nothing to rewrite.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if info, err := os.Lstat("root/scenes.tf"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("root/scenes.tf is no longer a link: %v, %v", info.Mode(), err)
	}
	if got, _ := os.ReadFile("shared/scenes.tf"); string(got) != migrateBefore {
		t.Fatal("shared/scenes.tf was changed")
	}
	out, err = runMigrate(t, "--write", "root", "shared")
	if err != nil || !strings.Contains(out, "root/scenes.tf is a symbolic link to shared/scenes.tf, which migrate reads instead.") || !strings.Contains(out, "Rewrote 1 file.") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile("shared/scenes.tf"); string(got) != migrateAfter {
		t.Fatalf("shared/scenes.tf is\n%s", got)
	}
}

// The local modules inside the named directories are read with them, also
// the modules that they call. A module outside them is only pointed at, in a
// directory of any name and with scenes in either syntax.
func TestMigrateModules(t *testing.T) {
	t.Chdir(t.TempDir())
	scene := "resource \"hue_scene\" \"s\" {\n  actions = { \"a\" = { kelvin = 2700 } }\n}\n"
	writeFiles(t, map[string]string{
		"root/main.tf":                  "module \"a\" {\n  source = \"./rooms/a\"\n}\nmodule \"b\" {\n  source = \"./rooms/[b]\"\n}\nmodule \"c\" {\n  source = \"../shared/[c]\"\n}\nmodule \"d\" {\n  source = \"../shared/d\"\n}\nmodule \"e\" {\n  source = \"../shared/e\"\n}\nmodule \"missing\" {\n  source = \"./missing\"\n}\n",
		"root/rooms/a/main.tf":          "module \"nested\" {\n  source = \"./nested\"\n}\nmodule \"again\" {\n  source = \"../[b]\"\n}\n",
		"root/rooms/a/nested/scenes.tf": scene,
		"root/rooms/[b]/scenes.tf":      scene,
		"shared/[c]/scenes.tf":          scene,
		"shared/d/scenes.tf.json":       `{"resource":{"hue_scene":{"d":{}}}}`,
		"shared/e/rooms.tf":             "resource \"hue_room\" \"e\" {\n}\n",
	})
	out, err := runMigrate(t, "root")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--- a/root/rooms/a/nested/scenes.tf",
		"--- a/root/rooms/[b]/scenes.tf",
		"module.c uses ../shared/[c], which is outside the directories that migrate reads and has hue_scene resources. Run hue-tf migrate 'shared/[c]' as well, if no configuration on provider 0.1 shares it.",
		"module.d uses ../shared/d",
		"root/missing is a local module that migrate could not read",
		"Read 3 local modules with the directories.",
		"Run hue-tf migrate --write root to rewrite 2 files.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// A module called twice is read once, and one without scenes is not
	// pointed at.
	if strings.Count(out, "--- a/root/rooms/[b]/scenes.tf") != 1 || strings.Contains(out, "module.e") || strings.Contains(out, "shared/[c]/scenes.tf\n") {
		t.Errorf("got\n%s", out)
	}
	if out, err = runMigrate(t, "--write", "root"); err != nil || !strings.Contains(out, "Rewrote 2 files.") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile("shared/[c]/scenes.tf"); string(got) != scene {
		t.Fatal("a module outside the directory was rewritten")
	}
}
