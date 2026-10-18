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
		"main.tf:2: module.room uses ./modules/room, which has hue_scene resources. Run hue-tf migrate modules/room as well.",
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
	if left, _ := filepath.Glob(".main.tf.*"); len(left) != 0 {
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
