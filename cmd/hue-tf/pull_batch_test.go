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
func TestBatchPreviewAndWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	src := []byte("resource \"hue_room\" \"room\" {\n name = \"Old\"\n archetype = \"bedroom\"\n children = []\n}\n")
	if e := os.WriteFile("room.tf", src, 0600); e != nil {
		t.Fatal(e)
	}
	b := fakebridge.New()
	defer b.Close()
	b.Put("room", id, hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: "New", Archetype: "bedroom"}})
	calls := []string{}
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return batchState(id, "Old"), nil }, terraform: func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show" {
			return []byte(`{"format_version":"1.2","resource_changes":[]}`), nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	if e := pullBatch(context.Background(), []string{id}, &out, deps); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile("room.tf")
	if !bytes.Equal(src, actual) || len(calls) != 0 {
		t.Fatal("preview mutated configuration/state")
	}
	if _, e := os.Stat(".hue-pull-baseline.json"); !os.IsNotExist(e) {
		t.Fatal("preview wrote baseline")
	}
	if e := pullBatch(context.Background(), []string{id, "--write"}, &out, deps); e != nil {
		t.Fatal(e)
	}
	actual, _ = os.ReadFile("room.tf")
	if !strings.Contains(string(actual), `"New"`) {
		t.Fatal(string(actual))
	}
	if strings.Join(calls, "\n") != "validate -no-color" {
		t.Fatal(calls)
	}
	if _, e := os.Stat(".hue-pull-transaction"); !os.IsNotExist(e) {
		t.Fatal("left transaction after success")
	}
	local := bytes.Replace(actual, []byte(`"New"`), []byte(`"Local"`), 1)
	if e := os.WriteFile("room.tf", local, 0600); e != nil {
		t.Fatal(e)
	}
	b.Put("room", id, hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: "App edit", Archetype: "bedroom"}})
	beforeCalls := len(calls)
	if e := pullBatch(context.Background(), []string{id, "--write"}, &out, deps); e == nil {
		t.Fatal("conflict should block write")
	}
	after, _ := os.ReadFile("room.tf")
	if len(calls) != beforeCalls || !bytes.Equal(local, after) {
		t.Fatal("blocked pull mutated files or state")
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal(r.Method)
		}
	}
}
func TestBatchNewCollisionDeletionAndCheckpoint(t *testing.T) {
	t.Chdir(t.TempDir())
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	remote := batchRemote(id, "Same")
	remote["room"][other] = batchRemote(other, "Same")["room"][other]
	plan, e := prepareBatch([]byte(`{"resources":[]}`), remote, pullNewOptions{}, pullCheckpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Creates) != 2 || len(plan.Problems) != 0 || plan.Creates[0].Address == plan.Creates[1].Address {
		t.Fatalf("%+v", plan)
	}
	source := "resource \"hue_room\" \"room\" {\n name = \"Local\"\n archetype = \"bedroom\"\n children = []\n}"
	if e = os.WriteFile("room.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	ancestor := map[string]json.RawMessage{"name": json.RawMessage(`"Old"`), "archetype": json.RawMessage(`"bedroom"`), "children": json.RawMessage(`[]`)}
	// A normal Terraform refresh must not erase the previous pull ancestor.
	state := batchState(id, "Remote")
	checkpoint := pullCheckpoint{Version: 1, Identity: pullIdentity(state), Resources: map[string]map[string]json.RawMessage{id: ancestor}}
	plan, e = prepareBatch(state, batchRemote(id, "Remote"), pullNewOptions{}, checkpoint)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Problems) == 0 || !strings.Contains(plan.Problems[0], "conflict") {
		t.Fatalf("%+v", plan)
	}
	if e = os.WriteFile("room.tf", []byte(strings.Replace(source, "Local", "Old", 1)), 0600); e != nil {
		t.Fatal(e)
	}
	gone := batchRemote(id, "Old")
	delete(gone["room"], id)
	plan, e = prepareBatch(batchState(id, "Old"), gone, pullNewOptions{}, pullCheckpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Problems) != 0 || len(plan.Removes) != 1 {
		t.Fatalf("%+v", plan)
	}
}
func TestBatchPendingImports(t *testing.T) {
	t.Chdir(t.TempDir())
	state := []byte(`{"resources":[]}`)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	remote := batchRemote(id, "Room")
	plan, e := prepareBatch(state, remote, pullNewOptions{}, pullCheckpoint{})
	if e != nil || len(plan.Problems) > 0 {
		t.Fatalf("%+v %v", plan, e)
	}
	deps := dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "validate -no-color" {
			t.Fatalf("unexpected command: %v", args)
		}
		return nil, nil
	}}
	if e = writeBatch(context.Background(), plan, state, &bytes.Buffer{}, deps); e != nil {
		t.Fatal(e)
	}
	if len(plan.ImportFiles) != 1 || !strings.Contains(string(plan.ImportFiles[0].Source), "to = hue_room.Room") {
		t.Fatalf("%+v", plan.ImportFiles)
	}
	again, e := prepareBatch(state, remote, pullNewOptions{}, plan.Checkpoint)
	if e != nil || len(again.Problems) > 0 || len(again.Creates) > 0 || len(again.ImportFiles) > 0 || len(again.Changes) > 0 {
		t.Fatalf("%+v %v", again, e)
	}
	delete(remote["room"], id)
	gone, e := prepareBatch(state, remote, pullNewOptions{}, plan.Checkpoint)
	if e != nil || len(gone.Problems) > 0 || len(gone.Removes) != 1 {
		t.Fatalf("%+v %v", gone, e)
	}
	if e = writeBatch(context.Background(), gone, state, &bytes.Buffer{}, deps); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{plan.ImportFiles[0].Path} {
		content, e := os.ReadFile(p)
		if e != nil || strings.Contains(string(content), " {") {
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
	plan, e := prepareBatch([]byte(`{"resources":[]}`), batchRemote("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "Room"), opts, pullCheckpoint{})
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
	plan := &batchPlan{ImportFiles: []pullCreation{{Path: path, Source: []byte("import {\n to = hue_room.new\n id = \"uuid\"\n}")}}}
	deps := dependencies{terraform: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "validate" {
			t.Fatalf("unexpected state operation: %v", args)
		}
		return nil, fmt.Errorf("invalid configuration")
	}}
	if e := writeBatch(context.Background(), plan, []byte(`{"resources":[]}`), &bytes.Buffer{}, deps); e == nil {
		t.Fatal("expected validation failure")
	}
	for _, p := range []string{path, ".hue-pull-transaction"} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("rollback left %s", p)
		}
	}
}

func TestBatchImportAndReferenceRetarget(t *testing.T) {
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
	plan, e := prepareBatch(state, remote, pullNewOptions{}, pullCheckpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Problems) > 0 {
		t.Fatal(plan.Problems)
	}
	found := false
	for _, c := range plan.Changes {
		if strings.Contains(string(c.Updated), "hue_scene.New.id") {
			found = true
		}
	}
	if len(plan.Creates) != 1 || !found {
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
	plan, e := prepareBatch(state, remote, pullNewOptions{}, pullCheckpoint{})
	if e != nil || len(plan.Problems) > 0 || len(plan.ImportFiles) != 1 {
		t.Fatalf("%+v %v", plan, e)
	}
	if filepath.Base(plan.ImportFiles[0].Path) != "imports_hue.tf" || strings.Count(string(plan.ImportFiles[0].Source), "import {") != 2 {
		t.Fatal(plan.ImportFiles)
	}
	if e = writeBatch(context.Background(), plan, state, &bytes.Buffer{}, dependencies{terraform: mockPullTerraform}); e != nil {
		t.Fatal(e)
	}
	// Remove one pending resource and add another in the same import file.
	delete(remote["room"], id)
	third := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	remote["room"][third] = batchRemote(third, "Three")["room"][third]
	next, e := prepareBatch(state, remote, pullNewOptions{}, plan.Checkpoint)
	if e != nil || len(next.Problems) > 0 || len(next.ImportFiles) != 0 {
		t.Fatalf("%+v %v", next, e)
	}
	if e = writeBatch(context.Background(), next, state, &bytes.Buffer{}, dependencies{terraform: mockPullTerraform}); e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile("imports_hue.tf")
	if e != nil || strings.Count(string(content), "import {") != 2 || strings.Contains(string(content), id) || !strings.Contains(string(content), third) {
		t.Fatalf("%s %v", content, e)
	}
	again, e := prepareBatch(state, remote, pullNewOptions{}, next.Checkpoint)
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
	plan, e := prepareBatch(state, remote, pullNewOptions{}, pullCheckpoint{})
	if e != nil || len(plan.Problems) > 0 || len(plan.Creates) != 1 {
		t.Fatalf("%+v %v", plan, e)
	}
	if e = writeBatch(context.Background(), plan, state, &bytes.Buffer{}, dependencies{terraform: func(context.Context, ...string) ([]byte, error) {
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
