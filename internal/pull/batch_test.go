package pull

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func syncAttrs(t *testing.T, src string) map[string]json.RawMessage {
	t.Helper()
	a, e := CanonicalAttributes([]byte(src))
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestSyncThreeWayReferences(t *testing.T) {
	dir := t.TempDir()
	original := `resource "hue_behavior_instance" "switch" {
 name = "Switch" # name comment
 enabled = true
 script_id = "script"
 configuration = jsonencode({
  group = hue_room.room.id # reference stays
  buttons = { one = { brightness = 20, label = "old" } }
 })
}
`
	if e := os.WriteFile(dir+"/switch.tf", []byte(original), 0600); e != nil {
		t.Fatal(e)
	}
	old := syncAttrs(t, strings.ReplaceAll(original, "hue_room.room.id", `"room-id"`))
	remote := strings.NewReplacer("brightness = 20", "brightness = 30", "hue_room.room.id", `"room-id"`).Replace(original)
	ctx := StateContext([]ManagedResource{{Address: "hue_room.room", Kind: "room", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"room-id"`)}}}, "")
	c, e := PrepareSync(dir, "behavior_instance", "switch", old, []byte(remote), ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(c.Updated), "brightness = 30") || !strings.Contains(string(c.Updated), "hue_room.room.id # reference stays") {
		t.Fatal(string(c.Updated))
	}
	// Independent local edits are retained, even inside the same jsonencode object.
	local := strings.Replace(original, `label = "old"`, `label = "local"`, 1)
	if e = os.WriteFile(dir+"/switch.tf", []byte(local), 0600); e != nil {
		t.Fatal(e)
	}
	c, e = PrepareSync(dir, "behavior_instance", "switch", old, []byte(remote), ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(c.Updated), `label = "local"`) {
		t.Fatal(string(c.Updated))
	}
	local = strings.Replace(local, "brightness = 20", "brightness = 40", 1)
	if e = os.WriteFile(dir+"/switch.tf", []byte(local), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = PrepareSync(dir, "behavior_instance", "switch", old, []byte(remote), ctx); e == nil || !strings.Contains(e.Error(), "conflict") {
		t.Fatalf("%v", e)
	}
}

func TestSyncMembershipAndCombinedFiles(t *testing.T) {
	dir := t.TempDir()
	original := `resource "hue_scene" "one" {
 name = "One"
 group = "group"
 actions = { a = { brightness = 10, on = true }, b = { on = false } }
}
resource "hue_room" "room" {
 name = "Room"
 archetype = "bedroom"
 children = ["b", "a"]
}
`
	if e := os.WriteFile(dir+"/main.tf", []byte(original), 0600); e != nil {
		t.Fatal(e)
	}
	sceneOld := syncAttrs(t, strings.Split(original, "resource \"hue_room\"")[0])
	remote := `resource "hue_scene" "one" {
 name = "Renamed"
 group = "group"
 actions = { a = { brightness = 25, on = true }, c = { on = true } }
}`
	c, e := PrepareSync(dir, "scene", "one", sceneOld, []byte(remote), StateContext(nil, ""))
	if e != nil {
		t.Fatal(e)
	}
	old := map[string]json.RawMessage{"name": json.RawMessage(`"Room"`), "archetype": json.RawMessage(`"bedroom"`), "children": json.RawMessage(`["a","b"]`)}
	removal, e := PrepareRemoval(dir, "room", "room", old, StateContext(nil, ""))
	if e != nil {
		t.Fatal(e)
	}
	merged, e := CombineChanges([]*Change{c, removal})
	if e != nil {
		t.Fatal(e)
	}
	if len(merged) != 1 || !strings.Contains(string(merged[0].Updated), "Renamed") || strings.Contains(string(merged[0].Updated), `"hue_room"`) {
		t.Fatal(string(merged[0].Updated))
	}
}
func TestSyncDeletionReferences(t *testing.T) {
	dir := t.TempDir()
	source := `output "keep" { value = hue_scene.gone.id }`
	if e := os.WriteFile(dir+"/main.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := CheckRemovals(map[string]string{"": dir}, nil, []ManagedResource{{Address: "hue_scene.gone"}})
	if e == nil || !strings.Contains(e.Error(), "references deleted") {
		t.Fatalf("%v", e)
	}
	if e = os.WriteFile(dir+"/main.tf", []byte("import {\n to = hue_scene.gone\n id = \"uuid\"\n}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	cs, e := CheckRemovals(map[string]string{"": dir}, nil, []ManagedResource{{Address: "hue_scene.gone"}})
	if e != nil || len(cs) != 1 {
		t.Fatalf("%v %v", cs, e)
	}
}

func TestSyncReferenceRetargetAndComputedTemperature(t *testing.T) {
	dir := t.TempDir()
	src := `resource "hue_behavior_instance" "switch" {
 name = "Switch"
 enabled = true
 script_id = "script"
 configuration = jsonencode({ scene = hue_scene.old.id })
}`
	if e := os.WriteFile(dir+"/switch.tf", []byte(src), 0600); e != nil {
		t.Fatal(e)
	}
	old := syncAttrs(t, strings.Replace(src, "hue_scene.old.id", `"old-id"`, 1))
	remote := strings.Replace(src, "hue_scene.old.id", `"new-id"`, 1)
	resources := []ManagedResource{
		{Address: "hue_scene.old", Kind: "scene", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"old-id"`)}},
		{Address: "hue_scene.new", Kind: "scene", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"new-id"`)}},
	}
	c, e := PrepareSync(dir, "behavior_instance", "switch", old, []byte(remote), StateContext(resources, ""))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(c.Updated), "hue_scene.new.id") {
		t.Fatal(string(c.Updated))
	}
	scene := `resource "hue_scene" "evening" {
 name = "Evening"
 group = "group"
 actions = { light = { kelvin = 2857 } }
}`
	if e = os.WriteFile(dir+"/scene.tf", []byte(scene), 0600); e != nil {
		t.Fatal(e)
	}
	baseline := syncAttrs(t, scene)
	baseline["actions"] = json.RawMessage(`{"light":{"kelvin":2857,"mirek":350,"color_xy":null,"color_hex":null,"brightness":null,"on":null}}`)
	canonical := strings.Replace(scene, "kelvin = 2857", "mirek = 350", 1)
	c, e = PrepareSync(dir, "scene", "evening", baseline, []byte(canonical), StateContext(nil, ""))
	if e != nil || len(c.Edits) != 0 {
		t.Fatalf("%+v %v", c, e)
	}
	if _, e = PrepareRemoval(dir, "scene", "evening", baseline, StateContext(nil, "")); e != nil {
		t.Fatal(e)
	}
}

func TestSyncTupleMembershipPreservesReferences(t *testing.T) {
	dir := t.TempDir()
	source := `resource "hue_behavior_instance" "switch" {
 name = "Switch"
 configuration = jsonencode({ scenes = [
  hue_scene.old.id, # keep this reference
 ] })
}`
	old := syncAttrs(t, strings.Replace(source, "hue_scene.old.id", `"old-id"`, 1))
	if e := os.WriteFile(dir+"/switch.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := StateContext([]ManagedResource{{Address: "hue_scene.old", Kind: "scene", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"old-id"`)}}}, "")
	remote := `resource "hue_behavior_instance" "switch" {
 name = "Switch"
 configuration = jsonencode({ scenes = ["old-id", "new-id"] })
}`
	c, e := PrepareSync(dir, "behavior_instance", "switch", old, []byte(remote), ctx)
	if e != nil {
		t.Fatal(e)
	}
	merged, e := CombineChanges([]*Change{c})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(merged[0].Updated), "hue_scene.old.id, # keep this reference") || !strings.Contains(string(merged[0].Updated), `"new-id"`) {
		t.Fatal(string(c.Updated))
	}
}

func TestSyncProviderEnvironment(t *testing.T) {
	dir := t.TempDir()
	source := `provider "hue" { application_key = "different-secret" }`
	if e := os.WriteFile(dir+"/main.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	e := CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key")
	if e == nil || strings.Contains(e.Error(), "different-secret") {
		t.Fatalf("%v", e)
	}
	if e = os.WriteFile(dir+"/main.tf", []byte(`provider "hue" {}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key"); e != nil {
		t.Fatal(e)
	}
}

func TestSyncHexPreservesUnchangedBridgeColor(t *testing.T) {
	dir := t.TempDir()
	source := `resource "hue_scene" "evening" {
 name = "Evening"
 actions = { light = { color_hex = "#2450ff", brightness = 2 } }
}`
	baseline := syncAttrs(t, source)
	baseline["actions"] = json.RawMessage(`{"light":{"color_hex":"#2450ff","color_xy":{"x":0.1449,"y":0.0828},"brightness":2}}`)
	remote := strings.Replace(source, `color_hex = "#2450ff"`, `color_xy = { x = 0.1448999, y = 0.0828 }`, 1)
	for _, tc := range []struct {
		name, local, remote string
		blocked             bool
	}{
		{"unchanged", source, remote, false},
		{"brightness", source, strings.Replace(remote, "brightness = 2", "brightness = 3", 1), false},
		{"local hex", strings.Replace(source, "#2450ff", "#6540ff", 1), remote, false},
		{"remote color", source, strings.Replace(remote, "0.1448999", "0.4", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(dir+"/scene.tf", []byte(tc.local), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := PrepareSync(dir, "scene", "evening", baseline, []byte(tc.remote), StateContext(nil, ""))
			if tc.blocked {
				if err == nil || !strings.Contains(err.Error(), "losslessly") {
					t.Fatalf("expected color block, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "brightness" {
				if !strings.Contains(string(c.Updated), "brightness = 3") || !strings.Contains(string(c.Updated), "#2450ff") {
					t.Fatal(string(c.Updated))
				}
			} else if len(c.Edits) != 0 {
				t.Fatalf("unexpected edits: %+v", c.Edits)
			}
			if _, err := PrepareRemoval(dir, "scene", "evening", baseline, StateContext(nil, "")); tc.name != "local hex" && err != nil {
				t.Fatal(err)
			}
		})
	}
}
