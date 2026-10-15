package pull

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeModuleFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestResourceAddress(t *testing.T) {
	kind, name, err := ResourceAddress("module.bedroom.module.scenes.hue_scene.night")
	if err != nil || kind != "scene" || name != "night" {
		t.Fatal(kind, name, err)
	}
	for _, addr := range []string{"module.a[0].hue_scene.night", "module.a.module.hue_scene.night", "module.a.hue_room.x[0]", "hue_device.lamp"} {
		if _, _, err := ResourceAddress(addr); err == nil {
			t.Fatal(addr)
		}
	}
}

// import-blocks only reads module sources, so the guards the old pull command
// needed for editing them must not stop it.
func TestConfigModules(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	writeModuleFile(t, filepath.Join(root, "main.tf"), `
module "bedroom" { source = "./rooms/bedroom" }
module "a" { source = "./shared" }
module "b" { source = "./shared" }
module "counted" {
  source = "./shared"
  count  = 2
}
module "each" {
  source   = "./nested"
  for_each = toset(["x"])
}
module "registry" { source = "example/hue-rooms/hue" }
module "outside" { source = "../outside" }
module "overridden" { source = "./before" }
`)
	writeModuleFile(t, filepath.Join(root, "override.tf"), `module "overridden" { source = "./after" }`)
	writeModuleFile(t, filepath.Join(root, "json.tf.json"), `{"module":{"json":{"source":"./shared"}}}`)
	writeModuleFile(t, filepath.Join(root, "rooms/bedroom/main.tf"), `module "scenes" { source = "./scenes" }`)
	writeModuleFile(t, filepath.Join(root, "rooms/bedroom/scenes/main.tf"), `resource "hue_scene" "night" {}`)
	writeModuleFile(t, filepath.Join(root, "shared/main.tf"), `resource "hue_room" "room" {}`)
	writeModuleFile(t, filepath.Join(root, "nested/main.tf"), `module "inner" { source = "../shared" }`)
	writeModuleFile(t, filepath.Join(root, "after/main.tf"), ``)
	writeModuleFile(t, filepath.Join(parent, "outside/main.tf"), ``)
	dirs, unusable, err := ConfigModules(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	for scope, rel := range map[string]string{
		"":                             "",
		"module.bedroom":               "rooms/bedroom",
		"module.bedroom.module.scenes": "rooms/bedroom/scenes",
		"module.a":                     "shared",
		"module.b":                     "shared",
		"module.counted":               "shared",
		"module.each":                  "nested",
		"module.each.module.inner":     "shared",
		"module.outside":               "../outside",
		"module.overridden":            "after",
		"module.json":                  "shared",
	} {
		if dirs[scope] != filepath.Join(resolved, rel) {
			t.Errorf("%s: got %q", scope, dirs[scope])
		}
	}
	if _, ok := dirs["module.registry"]; ok || !strings.Contains(unusable["module.registry"], "not a local directory") {
		t.Errorf("registry: %q %q", dirs["module.registry"], unusable["module.registry"])
	}
	for _, scope := range []string{"module.counted", "module.each", "module.each.module.inner"} {
		if !strings.Contains(unusable[scope], "count or for_each") {
			t.Errorf("%s: %q", scope, unusable[scope])
		}
	}
	for _, scope := range []string{"", "module.a", "module.bedroom", "module.json"} {
		if reason, ok := unusable[scope]; ok {
			t.Errorf("%s: %q", scope, reason)
		}
	}
}

func TestConfigModulesRejectsCycle(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, filepath.Join(root, "main.tf"), `module "a" { source = "./" }`)
	if _, _, err := ConfigModules(root); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatal(err)
	}
}

func TestDefinitions(t *testing.T) {
	dir := t.TempDir()
	writeModuleFile(t, filepath.Join(dir, "main.tf"), `
resource "hue_scene" "each" {
  for_each = toset(["a"])
  provider = hue.second
}
resource "hue_room" "counted" { count = 1 }
`)
	writeModuleFile(t, filepath.Join(dir, "main_override.tf"), `resource "hue_room" "counted" { name = "Counted" }`)
	writeModuleFile(t, filepath.Join(dir, "zones.tf.json"), `{"resource":{"hue_zone":{"upstairs":{"name":"Upstairs"}}}}`)
	writeModuleFile(t, filepath.Join(dir, ".backup.tf"), `resource "hue_zone" "ignored" {}`)
	got, err := Definitions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got["hue_scene.each"] || !got["hue_room.counted"] || !got["hue_zone.upstairs"] {
		t.Fatal(got)
	}
}

func TestModuleState(t *testing.T) {
	raw := []byte(`{"resources":[{"mode":"managed","module":"module.bedroom","type":"hue_room","name":"bedroom","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}]}]}`)
	if _, err := State(raw, "module.bedroom.hue_room.bedroom"); err != nil {
		t.Fatal(err)
	}
	if _, err := State(raw, "hue_room.bedroom"); err == nil {
		t.Fatal("matched wrong module")
	}
	_, groups, err := Inventory(raw, "module.bedroom")
	if err != nil || groups[sceneID] != "hue_room.bedroom" {
		t.Fatal(groups, err)
	}
	_, groups, err = Inventory(raw)
	if err != nil || len(groups) != 0 {
		t.Fatal("cross-module direct reference")
	}
	addresses, err := StateAddresses(raw)
	if err != nil || !addresses["module.bedroom.hue_room.bedroom"] || addresses["hue_room.bedroom"] {
		t.Fatal("wrong collision scope", addresses, err)
	}
}
