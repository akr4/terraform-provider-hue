package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
)

func batchState(id, name string) []byte {
	return []byte(fmt.Sprintf(`{"lineage":"test","resources":[{"mode":"managed","type":"hue_room","name":"room","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":%q,"name":%q,"archetype":"bedroom","children":[]}}]}]}`, id, name))
}
func batchRemote(id, name string) map[string]map[string]json.RawMessage {
	raw, _ := json.Marshal(hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: name, Archetype: "bedroom"}, Children: []hue.Reference{}})
	return map[string]map[string]json.RawMessage{"room": {id: raw}, "zone": {}, "scene": {}, "behavior_instance": {}}
}
func TestBatchManagedResourcesAreUntouched(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	src := []byte(`resource "hue_room" "room" { name = "Local" }`)
	if e := os.WriteFile("room.tf", src, 0600); e != nil {
		t.Fatal(e)
	}
	// Obsolete baselines must neither block discovery nor be overwritten.
	baseline := []byte("obsolete baseline")
	if e := os.WriteFile(".hue-pull-baseline.json", baseline, 0600); e != nil {
		t.Fatal(e)
	}
	b := fakebridge.New()
	defer b.Close()
	b.Put("room", id, hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: "App edit"}})
	deps := dependencies{newClient: func(string, string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return batchState(id, "Old"), nil }, terraform: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("unexpected Terraform command")
		return nil, nil
	}}
	for _, args := range [][]string{nil, {id, "--write"}, {"hue_room.room", "--write"}} {
		if e := importBlocks(context.Background(), args, &bytes.Buffer{}, deps); e != nil {
			t.Fatal(e)
		}
	}
	b.Remove("room", id)
	if e := importBlocks(context.Background(), []string{"--write"}, &bytes.Buffer{}, deps); e != nil {
		t.Fatal(e)
	}
	for path, want := range map[string][]byte{"room.tf": src, ".hue-pull-baseline.json": baseline} {
		actual, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(actual, want) {
			t.Fatalf("changed %s: %s %v", path, actual, e)
		}
	}
	if _, e := os.Stat(".hue-pull-transaction"); !os.IsNotExist(e) {
		t.Fatal("created transaction for no-op")
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal(r.Method)
		}
	}
}

// Import-blocks used to stop on any of these state entries, so it generated nothing.
func TestBatchComplexState(t *testing.T) {
	t.Chdir(t.TempDir())
	const newID = "90000000-0000-4000-8000-000000000000"
	state := []byte(`{"resources":[
 {"mode":"managed","type":"hue_device","name":"lamp","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"11111111-1111-4111-8111-111111111111"}}]},
 {"mode":"managed","type":"hue_zone","name":"counted","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"index_key":0,"attributes":{"id":"20000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_scene","name":"each","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"index_key":"day","attributes":{"id":"30000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_room","name":"replaced","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"40000000-0000-4000-8000-000000000000"}},{"deposed":"1a2b3c4d","attributes":{"id":"41000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_zone","name":"other_bridge","provider":"provider[\"registry.terraform.io/akr4/hue\"].second","instances":[{"attributes":{"id":"50000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","module":"module.rooms[\"office\"]","type":"hue_smart_scene","name":"natural","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"60000000-0000-4000-8000-000000000000"}}]}
]}`)
	remote := map[string]map[string]json.RawMessage{"room": {}, "zone": {}, "scene": {}, "smart_scene": {}, "behavior_instance": {}}
	for id, kind := range map[string]string{
		"20000000-0000-4000-8000-000000000000": "zone",
		"30000000-0000-4000-8000-000000000000": "scene",
		"40000000-0000-4000-8000-000000000000": "room",
		"41000000-0000-4000-8000-000000000000": "room",
		"50000000-0000-4000-8000-000000000000": "zone",
		"60000000-0000-4000-8000-000000000000": "smart_scene",
		newID:                                  "zone",
	} {
		raw, _ := json.Marshal(hue.Group{ID: id, Metadata: hue.Metadata{Name: "counted"}})
		remote[kind][id] = raw
	}
	plan, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(plan.Problems) > 0 {
		t.Fatalf("%+v %v", plan, e)
	}
	// hue_zone.counted is in state with count, so the new zone takes a UUID suffix.
	if len(plan.Creates) != 1 || plan.Creates[0].ID != newID || plan.Creates[0].Address != "hue_zone.counted_90000000_0000_4000_8000_000000000000" {
		t.Fatalf("%+v", plan.Creates)
	}
	plan, e = prepareImportBlocks(state, remote, pullNewOptions{selectors: []string{"hue_zone.counted[0]", "41000000-0000-4000-8000-000000000000"}})
	if e != nil || len(plan.Problems) > 0 || len(plan.Creates) != 0 || len(plan.Skipped) != 2 {
		t.Fatalf("%+v %v", plan, e)
	}
}

// Import-blocks used to stop on shared, registry and counted modules, and on
// override and JSON files, although it only appends to the root imports_hue.tf.
func TestBatchComplexModules(t *testing.T) {
	t.Chdir(t.TempDir())
	files := map[string]string{
		"main.tf": `provider "hue" {}
module "a" { source = "./shared" }
module "b" { source = "./shared" }
module "counted" {
  source = "./shared"
  count  = 1
}
module "registry" { source = "example/rooms/hue" }
`,
		"main_override.tf": `module "b" { source = "./shared" }`,
		"rooms.tf.json":    `{"resource":{"hue_room":{"Room":{"name":"Room"}}}}`,
		"shared/main.tf":   `resource "hue_scene" "evening" {}`,
	}
	for path, src := range files {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(src), 0600); e != nil {
			t.Fatal(e)
		}
	}
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	state := []byte(`{"resources":[]}`)
	plan, e := prepareImportBlocks(state, batchRemote(id, "Room"), pullNewOptions{})
	if e != nil || len(plan.Problems) > 0 {
		t.Fatalf("%+v %v", plan, e)
	}
	// hue_room.Room is declared in the JSON file, so the import takes a UUID suffix.
	if len(plan.Creates) != 1 || plan.Creates[0].Address != "hue_room.Room_aaaaaaaa_aaaa_4aaa_8aaa_aaaaaaaaaaaa" {
		t.Fatalf("%+v", plan.Creates)
	}
	opts, e := parsePullOptions([]string{"--module", "a"}, true)
	if e != nil {
		t.Fatal(e)
	}
	plan, e = prepareImportBlocks(state, batchRemote(id, "Room"), opts)
	if e != nil || len(plan.Creates) != 1 || plan.Creates[0].Address != "module.a.hue_room.Room" {
		t.Fatalf("%+v %v", plan, e)
	}
	for module, reason := range map[string]string{"counted": "count or for_each", "registry": "not a local directory"} {
		opts, e := parsePullOptions([]string{"--module", module}, true)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = prepareImportBlocks(state, batchRemote(id, "Room"), opts); e == nil || !strings.Contains(e.Error(), reason) {
			t.Fatalf("%s: %v", module, e)
		}
	}
}

func TestBatchNewNameCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	remote := batchRemote(id, "Same")
	remote["room"][other] = batchRemote(other, "Same")["room"][other]
	plan, e := prepareImportBlocks([]byte(`{"resources":[]}`), remote, pullNewOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Creates) != 2 || len(plan.Problems) != 0 || plan.Creates[0].Address == plan.Creates[1].Address {
		t.Fatalf("%+v", plan)
	}
}
func TestBatchPendingImports(t *testing.T) {
	t.Chdir(t.TempDir())
	state := []byte(`{"resources":[]}`)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	remote := batchRemote(id, "Room")
	plan, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(plan.Problems) > 0 {
		t.Fatalf("%+v %v", plan, e)
	}
	if e = writeImportBlocks(plan, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	if len(plan.ImportFiles) != 1 || !strings.Contains(string(plan.ImportFiles[0].Source), "to = hue_room.Room") {
		t.Fatalf("%+v", plan.ImportFiles)
	}
	again, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(again.Problems) > 0 || len(again.Creates) > 0 || len(again.ImportFiles) > 0 || len(again.Changes) > 0 {
		t.Fatalf("%+v %v", again, e)
	}
	delete(remote["room"], id)
	gone, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(gone.Problems) > 0 || len(gone.Changes) != 0 {
		t.Fatalf("%+v %v", gone, e)
	}
	if e = writeImportBlocks(gone, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{plan.ImportFiles[0].Path} {
		content, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(content, plan.ImportFiles[0].Source) {
			t.Fatalf("%s %v", content, e)
		}
	}
}
func TestBatchModulePlacement(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	root, _ = filepath.EvalSymlinks(root)
	if e := os.Mkdir("custom", 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile("main.tf", []byte(`module "washroom" { source = "./custom" }`), 0600); e != nil {
		t.Fatal(e)
	}
	opts, e := parsePullOptions([]string{"--module", "washroom", "--write"}, true)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := prepareImportBlocks([]byte(`{"resources":[]}`), batchRemote("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "Room"), opts)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Problems) != 0 || len(plan.Creates) != 1 || !strings.HasPrefix(plan.Creates[0].Address, "module.washroom.") {
		t.Fatalf("%+v", plan)
	}
	if len(plan.ImportFiles) != 1 || filepath.Dir(plan.ImportFiles[0].Path) != root || !strings.Contains(string(plan.ImportFiles[0].Source), "to = module.washroom.hue_room.Room") {
		t.Fatalf("%+v", plan.ImportFiles)
	}
}

func mockPullTerraform(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "show" {
		return []byte(`{"format_version":"1.2","resource_changes":[]}`), nil
	}
	return nil, nil
}

func TestBatchWriteRollback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root")
	}
	t.Chdir(t.TempDir())
	existing, _ := filepath.Abs("imports_hue.tf")
	const original = "# keep\n"
	if e := os.WriteFile(existing, []byte(original), 0600); e != nil {
		t.Fatal(e)
	}
	changes, e := pull.CombineChanges([]*pull.Change{{Path: existing, Original: []byte(original), Edits: []pull.Edit{{Start: len(original), End: len(original), After: "import {}\n", Field: "import blocks"}}}})
	if e != nil {
		t.Fatal(e)
	}
	// The edit to the existing file succeeds; the new file cannot be created.
	if e = os.Mkdir("locked", 0500); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod("locked", 0700) })
	locked, _ := filepath.Abs(filepath.Join("locked", "new.tf"))
	plan := &importBlocksPlan{Changes: changes, ImportFiles: []importCreation{{Path: locked, Source: []byte("import {}\n")}}}
	if e = writeImportBlocks(plan, &bytes.Buffer{}); e == nil {
		t.Fatal("expected a write failure")
	}
	if content, e := os.ReadFile(existing); e != nil || string(content) != original {
		t.Fatalf("not rolled back: %q %v", content, e)
	}
	for _, p := range []string{locked, ".hue-pull-transaction"} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("rollback left %s", p)
		}
	}
}

func TestBatchImportPreservesExistingReferences(t *testing.T) {
	t.Chdir(t.TempDir())
	sceneID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	newID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	switchID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	state := []byte(fmt.Sprintf(`{"resources":[
 {"mode":"managed","type":"hue_scene","name":"old","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":%q}}]},
 {"mode":"managed","type":"hue_behavior_instance","name":"switch","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":%q,"name":"Switch","enabled":true,"script_id":"script","configuration":%q}}]}
 ]}`, sceneID, switchID, `{"scene":"`+sceneID+`"}`))
	source := `resource "hue_behavior_instance" "switch" {
 name = "Switch"
 enabled = true
 script_id = "script"
 configuration = jsonencode({ scene = hue_scene.old.id })
}`
	if e := os.WriteFile("switch.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	// Focus the selected module using valid existing scene configuration too.
	sceneSource := `resource "hue_scene" "old" {
 name = "Old"
 group = "group"
 actions = {}
}`
	if e := os.WriteFile("scene.tf", []byte(sceneSource), 0600); e != nil {
		t.Fatal(e)
	}
	scene := func(id, name string) json.RawMessage {
		data, _ := json.Marshal(hue.Scene{ID: id, Metadata: hue.Metadata{Name: name}, Group: hue.Reference{RID: "group", RType: "room"}, Actions: []hue.SceneAction{}})
		return data
	}
	behavior, _ := json.Marshal(hue.BehaviorInstance{ID: switchID, Type: "behavior_instance", Metadata: hue.Metadata{Name: "Switch"}, Enabled: true, ScriptID: "script", Configuration: json.RawMessage(`{"scene":"` + newID + `"}`)})
	remote := map[string]map[string]json.RawMessage{"scene": {sceneID: scene(sceneID, "Old"), newID: scene(newID, "New")}, "behavior_instance": {switchID: behavior}}
	plan, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Problems) > 0 {
		t.Fatal(plan.Problems)
	}
	if len(plan.Creates) != 1 || len(plan.Changes) != 0 {
		t.Fatalf("%+v", plan)
	}

}

func TestBatchStateReader(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		read := stateReader(dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
			calls++
			if args[0] == "state" {
				if fail {
					return nil, fmt.Errorf("backend unavailable")
				}
				return nil, nil
			}
			return []byte(`{"format_version":"1.0"}`), nil
		}})
		data, e := read(context.Background())
		if fail {
			if e == nil || calls != 1 {
				t.Fatalf("failure hidden: %s %v (%d calls)", data, e, calls)
			}
		} else if e != nil || string(data) != `{"resources":[]}` {
			t.Fatalf("%s %v", data, e)
		}
	}
	// An empty state pull that show -json contradicts used to report "<nil>".
	for shown, want := range map[string]string{
		`{"format_version":"1.0","values":{"root_module":{}}}`: "terraform show -json lists resources",
		`not json`: "terraform show -json returned invalid JSON",
	} {
		read := stateReader(dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
			if args[0] == "state" {
				return nil, nil
			}
			return []byte(shown), nil
		}})
		if _, e := read(context.Background()); e == nil || !strings.Contains(e.Error(), want) || strings.Contains(e.Error(), "<nil>") {
			t.Fatalf("%q: %v", shown, e)
		}
	}
}

// A missing terraform used to fail with "terraform state pull failed: " and nothing after it.
func TestTerraformCommandErrors(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, e := terraformCommand(context.Background(), "state", "pull")
	if e == nil || !strings.Contains(e.Error(), "terraform state pull failed") || !strings.Contains(e.Error(), "executable file not found") || !strings.Contains(e.Error(), "add it to PATH") {
		t.Fatalf("%v", e)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Error: backend locked' >&2\nexit 1\n"
	if e = os.WriteFile(filepath.Join(bin, "terraform"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin)
	_, e = terraformCommand(context.Background(), "state", "pull")
	if e == nil || !strings.Contains(e.Error(), "exit status 1") || !strings.Contains(e.Error(), "Error: backend locked") {
		t.Fatalf("%v", e)
	}
}

func TestBatchCombinedImportFile(t *testing.T) {
	t.Chdir(t.TempDir())
	state := []byte(`{"resources":[]}`)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	remote := batchRemote(id, "One")
	remote["room"][other] = batchRemote(other, "Two")["room"][other]
	plan, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(plan.Problems) > 0 || len(plan.ImportFiles) != 1 {
		t.Fatalf("%+v %v", plan, e)
	}
	if filepath.Base(plan.ImportFiles[0].Path) != "imports_hue.tf" || strings.Count(string(plan.ImportFiles[0].Source), "import {") != 2 {
		t.Fatal(plan.ImportFiles)
	}
	if e = writeImportBlocks(plan, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	// Remove one pending resource and add another in the same import file.
	delete(remote["room"], id)
	third := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	remote["room"][third] = batchRemote(third, "Three")["room"][third]
	next, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(next.Problems) > 0 || len(next.ImportFiles) != 0 {
		t.Fatalf("%+v %v", next, e)
	}
	if e = writeImportBlocks(next, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile("imports_hue.tf")
	if e != nil || strings.Count(string(content), "import {") != 3 || !strings.Contains(string(content), id) || !strings.Contains(string(content), third) {
		t.Fatalf("%s %v", content, e)
	}
	// Appending used to leave a .hue-pull-backup-* copy behind on every success.
	if leftovers, _ := filepath.Glob(".hue-pull-*"); len(leftovers) != 0 {
		t.Fatalf("left %v", leftovers)
	}
	again, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(again.Problems) > 0 || len(again.Changes) > 0 || len(again.Creates) > 0 {
		t.Fatalf("%+v %v", again, e)
	}
}

func TestBatchImportOnlyUnsupportedNewResource(t *testing.T) {
	t.Chdir(t.TempDir())
	state := []byte(`{"resources":[]}`)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	// Import discovery should not depend on hue-tf's action serializer.
	remote := map[string]map[string]json.RawMessage{"scene": {id: json.RawMessage(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","metadata":{"name":"New"},"actions":[{"unsupported":true}]}`)}}
	plan, e := prepareImportBlocks(state, remote, pullNewOptions{})
	if e != nil || len(plan.Problems) > 0 || len(plan.Creates) != 1 {
		t.Fatalf("%+v %v", plan, e)
	}
	if e = writeImportBlocks(plan, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob("*.tf")
	if e != nil || len(files) != 1 || files[0] != "imports_hue.tf" {
		t.Fatalf("%v %v", files, e)
	}
}
