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
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return batchState(id, "Old"), nil }, terraform: func(context.Context, ...string) ([]byte, error) {
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
	deps := dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "validate -no-color" {
			t.Fatalf("unexpected command: %v", args)
		}
		return nil, nil
	}}
	if e = writeImportBlocks(context.Background(), plan, &bytes.Buffer{}, deps); e != nil {
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
	if e = writeImportBlocks(context.Background(), gone, &bytes.Buffer{}, deps); e != nil {
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

func TestBatchValidateRollback(t *testing.T) {
	t.Chdir(t.TempDir())
	path, _ := filepath.Abs("new.tf")
	plan := &importBlocksPlan{ImportFiles: []importCreation{{Path: path, Source: []byte("import {\n to = hue_room.new\n id = \"uuid\"\n}")}}}
	deps := dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "validate" {
			t.Fatalf("unexpected state operation: %v", args)
		}
		return nil, fmt.Errorf("invalid configuration")
	}}
	if e := writeImportBlocks(context.Background(), plan, &bytes.Buffer{}, deps); e == nil {
		t.Fatal("expected validation failure")
	}
	for _, p := range []string{path, ".hue-pull-transaction"} {
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
	if e = writeImportBlocks(context.Background(), plan, &bytes.Buffer{}, dependencies{terraform: mockPullTerraform}); e != nil {
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
	if e = writeImportBlocks(context.Background(), next, &bytes.Buffer{}, dependencies{terraform: mockPullTerraform}); e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile("imports_hue.tf")
	if e != nil || strings.Count(string(content), "import {") != 3 || !strings.Contains(string(content), id) || !strings.Contains(string(content), third) {
		t.Fatalf("%s %v", content, e)
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
	if e = writeImportBlocks(context.Background(), plan, &bytes.Buffer{}, dependencies{terraform: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("must defer validation until config generation")
		return nil, nil
	}}); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob("*.tf")
	if e != nil || len(files) != 1 || files[0] != "imports_hue.tf" {
		t.Fatalf("%v %v", files, e)
	}
}
