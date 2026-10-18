package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

// Upgrading from v0.1 with Terraform CLI: the configuration that v0.1 used
// fails before anything is sent, and once it is rewritten, the state that
// v0.1 wrote upgrades and plans no changes. The palette, gradient, effects_v2
// and dynamics were JSON strings in v0.1.
func TestAccSceneUpgradeFromV01(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	const roomID = "44444444-4444-4444-8444-444444444444"
	const colorsID = "55555555-5555-4555-8555-555555555555"
	const plainID = "66666666-6666-4666-8666-666666666666"
	const candleID = "77777777-7777-4777-8777-777777777777"
	b.Put("room", roomID, hue.Group{ID: roomID, Type: "room", Metadata: hue.Metadata{Name: "Study", Archetype: "other"}, Children: []hue.Reference{{RID: fakebridge.DeviceID, RType: "device"}}})
	// The bridge returns empty palette lists for a scene without a palette.
	const emptyPalette = `{"color":[],"color_temperature":[],"dimming":[],"effects":[],"effects_v2":[]}`
	// v0.1 sent the JSON that the configuration wrote, here without a gradient
	// mode, and kept it in state.
	const gradient = `{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}},{"color":{"xy":{"x":0.4,"y":0.4}}}]}`
	const dynamics = `{"duration":800}`
	const effect = `{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`
	const oldEffect = `{"effect":"candle"}`
	speed, dynamic := 0.5, false
	b.Put("scene", colorsID, hue.Scene{ID: colorsID, Type: "scene", Metadata: hue.Metadata{Name: "Colors"}, Group: hue.Reference{RID: roomID, RType: "room"}, Speed: &speed, AutoDynamic: &dynamic,
		Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 25}, ColorTemperature: &hue.Temperature{Mirek: 370}, Dynamics: json.RawMessage(dynamics)}}},
		Palette: json.RawMessage(`{"color":[{"color":{"xy":{"x":0.3,"y":0.4}},"dimming":{"brightness":25}}],"color_temperature":[],"dimming":[],"effects":[],"effects_v2":[]}`)})
	b.Put("scene", plainID, hue.Scene{ID: plainID, Type: "scene", Metadata: hue.Metadata{Name: "Plain"}, Group: hue.Reference{RID: roomID, RType: "room"}, Speed: &speed, AutoDynamic: &dynamic,
		Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Gradient: json.RawMessage(gradient)}}},
		Palette: json.RawMessage(emptyPalette)})
	b.Put("scene", candleID, hue.Scene{ID: candleID, Type: "scene", Metadata: hue.Metadata{Name: "Candle"}, Group: hue.Reference{RID: roomID, RType: "room"}, Speed: &speed, AutoDynamic: &dynamic,
		Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, EffectsV2: json.RawMessage(effect), Effects: json.RawMessage(oldEffect)}}},
		Palette: json.RawMessage(emptyPalette)})
	dir, cli := terraformCLI(t, b)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scenes := func(palette, colors, plain, candle string) string {
		return fmt.Sprintf(`resource "hue_scene" "colors" {
 name = "Colors"
 group = %[1]q
 palette = %[3]s
 actions = {
  %[2]q = { on = true, brightness = 25, mirek = 370, %[4]s }
 }
}
resource "hue_scene" "plain" {
 name = "Plain"
 group = %[1]q
 actions = {
  %[2]q = { on = true, %[5]s }
 }
}
resource "hue_scene" "candle" {
 name = "Candle"
 group = %[1]q
 actions = {
  %[2]q = { on = true, %[6]s }
 }
}
`, roomID, fakebridge.LightID, palette, colors, plain, candle)
	}
	// The state that v0.1 writes: the palette that the configuration wrote, as
	// jsonencode returns it, and the bridge's palette for the other scenes.
	instance := func(id, name, palette string, action map[string]any) map[string]any {
		return map[string]any{"schema_version": 2, "sensitive_attributes": []any{}, "attributes": map[string]any{
			"id": id, "name": name, "group": roomID, "speed": speed, "auto_dynamic": dynamic, "palette": palette,
			"actions": map[string]any{fakebridge.LightID: action},
		}}
	}
	action := func(brightness, mirek, kelvin any, objects map[string]any) map[string]any {
		a := map[string]any{"gradient": nil, "effects": nil, "effects_v2": nil, "dynamics": nil, "on": true, "brightness": brightness, "mirek": mirek, "kelvin": kelvin, "color_xy": nil}
		for k, v := range objects {
			a[k] = v
		}
		return a
	}
	resource := func(name string, instance map[string]any) map[string]any {
		return map[string]any{"mode": "managed", "type": "hue_scene", "name": name, "provider": `provider["registry.terraform.io/akr4/hue"]`, "instances": []any{instance}}
	}
	state, err := json.Marshal(map[string]any{"version": 4, "terraform_version": "1.10.5", "serial": 1, "lineage": "00000000-0000-4000-8000-000000000001", "outputs": map[string]any{}, "check_results": nil, "resources": []any{
		resource("colors", instance(colorsID, "Colors", `{"color":[{"color":{"xy":{"x":0.3,"y":0.4}},"dimming":{"brightness":25}}]}`, action(25, 370, 2703, map[string]any{"dynamics": dynamics}))),
		resource("plain", instance(plainID, "Plain", emptyPalette, action(nil, nil, nil, map[string]any{"gradient": gradient}))),
		resource("candle", instance(candleID, "Candle", emptyPalette, action(nil, nil, nil, map[string]any{"effects_v2": effect, "effects": oldEffect}))),
	}})
	if err != nil {
		t.Fatal(err)
	}
	write("terraform.tfstate", string(state))
	plan := func() (string, int) {
		t.Helper()
		out, err := cli("plan", "-input=false", "-no-color", "-detailed-exitcode")
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}

	// The configuration that v0.1 used is a type error.
	write("scenes.tf", scenes(`jsonencode({ color = [{ color = { xy = { x = 0.3, y = 0.4 } }, dimming = { brightness = 25 } }] })`,
		`dynamics = jsonencode({ duration = 800 })`,
		`gradient = jsonencode({ points = [{ color = { xy = { x = 0.3, y = 0.4 } } }, { color = { xy = { x = 0.4, y = 0.4 } } }] })`,
		`effects_v2 = jsonencode({ action = { effect = "candle", parameters = { color_temperature = { mirek = 370 }, speed = 0.4 } } }), effects = jsonencode({ effect = "candle" })`))
	out, code := plan()
	if code != 1 || !strings.Contains(out, `Inappropriate value for attribute "palette": object required.`) {
		t.Fatalf("plan exited with %d:\n%s", code, out)
	}
	for _, attribute := range []string{"gradient", "dynamics", "effects_v2"} {
		if !regexp.MustCompile(`attribute "` + attribute + `": object\s+required`).MatchString(out) {
			t.Errorf("plan did not report %s:\n%s", attribute, out)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "terraform.tfstate")); !bytes.Equal(got, state) {
		t.Fatalf("a failed plan changed the state:\n%s", got)
	}

	// Rewritten, it plans no changes, and apply writes the upgraded state.
	write("scenes.tf", scenes(`{ color = [{ color_xy = { x = 0.3, y = 0.4 }, brightness = 25 }] }`,
		`dynamics = { duration = 800 }`,
		`gradient = { points = [{ x = 0.3, y = 0.4 }, { x = 0.4, y = 0.4 }] }`,
		`effects_v2 = { effect = "candle", parameters = { mirek = 370, speed = 0.4 } }, effects = jsonencode({ effect = "candle" })`))
	if out, code := plan(); code != 0 {
		t.Fatalf("plan exited with %d:\n%s", code, out)
	}
	if out, err := cli("apply", "-input=false", "-no-color", "-auto-approve"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	var upgraded struct {
		Resources []struct {
			Name      string
			Instances []struct {
				SchemaVersion int `json:"schema_version"`
				Attributes    struct {
					Palette json.RawMessage
					Actions map[string]map[string]json.RawMessage
				}
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &upgraded); err != nil || len(upgraded.Resources) != 3 {
		t.Fatalf("state %s: %v", raw, err)
	}
	want := map[string]string{
		"colors": `{"color":[{"brightness":25,"color_xy":{"x":0.3,"y":0.4}}],"color_temperature":null,"dimming":null,"effects_v2":null}`,
		"plain":  `null`,
		"candle": `null`,
	}
	wantObjects := map[string]map[string]string{
		"colors": {"gradient": `null`, "effects_v2": `null`, "dynamics": `{"duration":800}`, "effects": `null`},
		// The mode is the API's default.
		"plain":  {"gradient": `{"mode":"interpolated_palette","points":[{"x":0.3,"y":0.4},{"x":0.4,"y":0.4}]}`, "effects_v2": `null`, "dynamics": `null`, "effects": `null`},
		"candle": {"gradient": `null`, "effects_v2": `{"effect":"candle","parameters":{"color_xy":null,"mirek":370,"speed":0.4}}`, "dynamics": `null`, "effects": mustJSON(t, oldEffect)},
	}
	for _, r := range upgraded.Resources {
		got := r.Instances[0]
		if got.SchemaVersion != 4 || compactJSON(t, got.Attributes.Palette) != want[r.Name] {
			t.Errorf("%s upgraded to version %d with palette %s", r.Name, got.SchemaVersion, got.Attributes.Palette)
		}
		action := got.Attributes.Actions[fakebridge.LightID]
		for attribute, w := range wantObjects[r.Name] {
			if g := compactJSON(t, action[attribute]); g != w {
				t.Errorf("%s upgraded with %s %s, want %s", r.Name, attribute, g, w)
			}
		}
	}
	if out, code := plan(); code != 0 {
		t.Fatalf("plan after apply exited with %d:\n%s", code, out)
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatalf("upgrading wrote to the bridge: %s %s %s", r.Method, r.Path, r.Body)
		}
	}
}

func compactJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return mustJSON(t, v)
}
