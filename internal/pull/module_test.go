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
func TestModuleResolution(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, filepath.Join(root, "main.tf"), `module "bedroom" { source = "./rooms/bedroom" }`)
	writeModuleFile(t, filepath.Join(root, "rooms/bedroom/main.tf"), `module "scenes" { source = "./scenes" }`)
	writeModuleFile(t, filepath.Join(root, "rooms/bedroom/scenes/main.tf"), source)
	addr := "module.bedroom.module.scenes.hue_scene.night"
	kind, name, err := ResourceAddress(addr)
	if err != nil || kind != "scene" || name != "night" {
		t.Fatal(kind, name, err)
	}
	dir, err := ModuleDir(root, addr)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.EvalSymlinks(filepath.Join(root, "rooms/bedroom/scenes"))
	if dir != expected {
		t.Fatal(dir)
	}
	c, err := Prepare(dir, name, baseline(), scene())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Path, expected) {
		t.Fatal("wrong module edited")
	}
}
func TestModuleGuards(t *testing.T) {
	for _, tc := range []struct{ name, config string }{
		{"shared", `module "a" { source = "./shared" }
module "b" { source = "./shared" }`},
		{"count", `module "a" {
source = "./shared"
count = 2
}`},
		{"remote", `module "a" { source = "github.com/example/modules" }`},
		{"cycle", `module "a" { source = "./" }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeModuleFile(t, filepath.Join(root, "main.tf"), tc.config)
			writeModuleFile(t, filepath.Join(root, "shared/main.tf"), source)
			if _, err := ModuleDir(root, "module.a.hue_scene.night"); err == nil {
				t.Fatal("accepted unsupported module")
			}
		})
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	writeModuleFile(t, filepath.Join(root, "main.tf"), `module "a" { source = "../outside" }`)
	writeModuleFile(t, filepath.Join(parent, "outside/main.tf"), source)
	if _, err := ModuleDir(root, "module.a.hue_scene.night"); err == nil {
		t.Fatal("escaped root")
	}
	for _, addr := range []string{"module.a[0].hue_scene.night", "module.a.module.hue_scene.night", "module.a.hue_room.x[0]"} {
		if _, _, err := ResourceAddress(addr); err == nil {
			t.Fatal(addr)
		}
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
	if !AddressReserved(raw, "room", "bedroom", "module.bedroom") || AddressReserved(raw, "room", "bedroom") {
		t.Fatal("wrong collision scope")
	}
}
