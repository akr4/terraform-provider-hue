package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
)

// Exercise the actual CLI output without editing generated HCL. All resources
// already exist in fakebridge; importing and replanning must never write to it.
func TestAccGeneratedConfig(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run Terraform CLI integration")
	}
	binary := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("terraform")
		if err != nil {
			t.Fatal(err)
		}
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	configs := make(chan *plugin.ReattachConfig, 1)
	closed := make(chan struct{})
	served := make(chan error, 1)
	const address = "registry.terraform.io/akr4/hue"
	go func() {
		served <- tf6server.Serve(address, providerserver.NewProtocol6(&hueProvider{version: "test", client: b.Client()}), tf6server.WithDebug(ctx, configs, closed), tf6server.WithLoggingSink(t))
	}()
	var cfg *plugin.ReattachConfig
	select {
	case cfg = <-configs:
	case err := <-served:
		t.Fatalf("provider startup: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		cancel()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("provider did not stop")
		}
	}()
	reattach, _ := json.Marshal(map[string]any{address: map[string]any{"Protocol": cfg.Protocol, "ProtocolVersion": 6, "Pid": cfg.Pid, "Test": true, "Addr": map[string]string{"Network": cfg.Addr.Network(), "String": cfg.Addr.String()}}})
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("terraform.rc", fmt.Sprintf("provider_installation {\n dev_overrides {\n \"akr4/hue\" = %q\n }\n}\n", dir))
	write("provider.tf", `terraform {
 required_providers {
 hue = { source = "akr4/hue" }
 }
}`+"\n"+accProvider)
	imports := ""
	for _, r := range []struct{ address, id string }{{"hue_device.test", fakebridge.DeviceID}, {"hue_room.test", smartGroupID}, {"hue_zone.test", zoneID}, {"hue_scene.temperature", smartDaySceneID}, {"hue_scene.color", smartNightSceneID}, {"hue_scene.effect", effectID}, {"hue_smart_scene.test", smartID}, {"hue_behavior_instance.test", behaviorTestID}} {
		imports += fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", r.address, r.id)
	}
	write("imports.tf", imports)
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "TF_") && !strings.HasPrefix(e, "HUE_") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "TF_CLI_CONFIG_FILE="+filepath.Join(dir, "terraform.rc"), "TF_REATTACH_PROVIDERS="+string(reattach))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("plan", "-input=false", "-no-color", "-generate-config-out=generated.tf", "-out=import.tfplan")
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
