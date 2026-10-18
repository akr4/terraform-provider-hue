package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

// Exercise the actual CLI output without editing generated HCL. All resources
// already exist in fakebridge; importing and replanning must never write to it.
func TestAccGeneratedConfig(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	seedSmartSceneGroup(b)
	zoneID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	b.Put("zone", zoneID, hue.Group{ID: zoneID, Type: "zone", Metadata: hue.Metadata{Name: "Zone", Archetype: "other"}, Children: []hue.Reference{{RID: fakebridge.LightID, RType: "light"}}})
	b.Put("room", smartGroupID, hue.Group{ID: smartGroupID, Type: "room", Metadata: hue.Metadata{Name: "Room", Archetype: "other"}, Children: []hue.Reference{{RID: fakebridge.DeviceID, RType: "device"}}})
	speed, dynamic := 0.5, false
	effectID := "99999999-9999-4999-8999-999999999999"
	for _, id := range []string{smartDaySceneID, smartNightSceneID, effectID} {
		action := hue.Action{On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 25}}
		if id == smartDaySceneID {
			action.ColorTemperature = &hue.Temperature{Mirek: 300}
		} else if id == effectID {
			action.Effects = json.RawMessage(`{"effect":"prism"}`)
			action.EffectsV2 = json.RawMessage(`{"action":{"effect":"prism","parameters":{"speed":0.4}}}`)
			action.Dynamics = json.RawMessage(`{"duration":800}`)
		} else {
			action.Gradient = json.RawMessage(`{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}},{"color":{"xy":{"x":0.4,"y":0.4}}}],"mode":"interpolated_palette"}`)
			action.Color = &hue.ActionColor{XY: hue.XY{X: 0.3, Y: 0.4}}
		}
		b.Put("scene", id, hue.Scene{ID: id, Type: "scene", Metadata: hue.Metadata{Name: "Scene"}, Group: hue.Reference{RID: smartGroupID, RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: action}}, Speed: &speed, AutoDynamic: &dynamic, Palette: json.RawMessage(`{"color":[{"color":{"xy":{"x":0.3,"y":0.4}},"dimming":{"brightness":25}}],"dimming":[],"color_temperature":[]}`)})
	}
	smartID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	b.Put("smart_scene", smartID, hue.SmartScene{ID: smartID, Type: "smart_scene", Metadata: hue.Metadata{Name: "Natural"}, Group: hue.Reference{RID: smartGroupID, RType: "room"}, State: "inactive", TransitionDuration: 60000, WeekTimeslots: []hue.SmartDay{{Recurrence: []string{"monday"}, Timeslots: []hue.SmartSlot{{StartTime: hue.SmartStart{Kind: "sunset"}, Target: hue.Reference{RID: smartNightSceneID, RType: "scene"}}}}}})
	b.Put("behavior_instance", behaviorTestID, hue.BehaviorInstance{ID: behaviorTestID, Type: "behavior_instance", Metadata: hue.Metadata{Name: "Switch"}, ScriptID: behaviorScriptID, Enabled: true, Status: "running", Configuration: json.RawMessage(`{"enabled":true,"nested":{"values":[1,null,false]}}`)})
	dir, cli := terraformCLI(t, b)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	imports := ""
	for _, r := range []struct{ address, id string }{{"hue_device.test", fakebridge.DeviceID}, {"hue_room.test", smartGroupID}, {"hue_zone.test", zoneID}, {"hue_scene.temperature", smartDaySceneID}, {"hue_scene.color", smartNightSceneID}, {"hue_scene.effect", effectID}, {"hue_smart_scene.test", smartID}, {"hue_behavior_instance.test", behaviorTestID}} {
		imports += fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", r.address, r.id)
	}
	write("imports.tf", imports)
	run := func(args ...string) []byte {
		t.Helper()
		out, err := cli(args...)
		if err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("plan", "-input=false", "-no-color", "-generate-config-out=generated.tf", "-out=import.tfplan")
	// The palette and the action objects are generated as objects, not as JSON.
	generated, err := os.ReadFile(filepath.Join(dir, "generated.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, attribute := range []string{"palette", "gradient", "effects_v2", "dynamics"} {
		if !regexp.MustCompile(`(?m)^\s*` + attribute + `\s*=\s*\{`).Match(generated) {
			t.Errorf("generated configuration should write %s as an object:\n%s", attribute, generated)
		}
	}
	var plan struct {
		ResourceChanges []struct {
			Address string
			Change  struct {
				Actions   []string
				Importing *struct{ ID string }
			}
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(run("show", "-json", "import.tfplan"), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.ResourceChanges) != 8 {
		t.Fatalf("expected 8 imports, got %d", len(plan.ResourceChanges))
	}
	for _, change := range plan.ResourceChanges {
		if change.Change.Importing == nil || change.Change.Importing.ID == "" || len(change.Change.Actions) != 1 || change.Change.Actions[0] != "no-op" {
			t.Fatalf("expected import without changes for %s: %+v", change.Address, change.Change)
		}
	}
	run("apply", "-input=false", "-no-color", "import.tfplan")
	if err := os.Remove(filepath.Join(dir, "imports.tf")); err != nil {
		t.Fatal(err)
	}
	run("plan", "-input=false", "-no-color", "-detailed-exitcode")
	for _, req := range b.Requests() {
		if req.Method != "GET" {
			t.Fatalf("import/generated config mutated bridge: %s %s", req.Method, req.Path)
		}
	}
}
