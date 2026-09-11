package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestSceneJSONActionModel(t *testing.T) {
	old := emptyAction()
	old.Gradient = types.StringValue(`{"points": [], "mode":"interpolated_palette"}`)
	old.Effects = types.StringValue(`{"effect":"fire"}`)
	actual := hue.Action{Gradient: json.RawMessage(`{"mode":"interpolated_palette","points":[]}`), Effects: json.RawMessage(`{"effect":"candle"}`)}
	next := reconcileAction(old, actual, hue.Light{})
	if !next.Gradient.Equal(old.Gradient) || next.Effects.ValueString() != `{"effect":"candle"}` {
		t.Fatal(next)
	}
	planned := planAction(emptyAction(), emptyAction(), old)
	if !planned.Gradient.Equal(old.Gradient) || !planned.Effects.Equal(old.Effects) {
		t.Fatal("omitted JSON actions lost")
	}
	bad := emptyAction()
	bad.Gradient = types.StringValue(`[]`)
	if len(validateAction(bad)) == 0 {
		t.Fatal("accepted non-object")
	}
}
func TestAccSceneJSONActions(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	groupID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	b.Put("room", groupID, hue.Group{ID: groupID, Type: "room", Metadata: hue.Metadata{Name: "Room", Archetype: "bedroom"}})
	scene := hue.Scene{ID: id, Metadata: hue.Metadata{Name: "JSON actions"}, Group: hue.Reference{RID: groupID, RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Gradient: json.RawMessage(`{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}}],"mode":"interpolated_palette"}`), Effects: json.RawMessage(`{"effect":"fire"}`)}}}}
	speed, dynamic := 1.0, false
	scene.Speed, scene.AutoDynamic = &speed, &dynamic
	scene.Palette = json.RawMessage(`{"color":[],"dimming":[],"color_temperature":[]}`)
	b.Put("scene", id, scene)
	config := func(effect string, explicit bool) string {
		extras := ""
		if explicit {
			extras = fmt.Sprintf(`gradient = jsonencode({ points = [{ color = { xy = { x = 0.3, y = 0.4 } } }], mode = "interpolated_palette" })
 effects = jsonencode({ effect = %q })`, effect)
		}
		return accProvider + fmt.Sprintf(`
resource "hue_scene" "test" {
 name = %q
 group = %q
 actions = { %q = { on = true
 %s
 } }
}
`, "JSON actions "+effect, groupID, fakebridge.LightID, extras)
	}
	check := func(effect string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			got, err := b.ReadScene(context.Background(), id)
			if err != nil {
				return err
			}
			if !sameJSON(got.Actions[0].Action.Gradient, scene.Actions[0].Action.Gradient) || !sameJSON(got.Actions[0].Action.Effects, []byte(fmt.Sprintf(`{"effect":%q}`, effect))) {
				return fmt.Errorf("JSON action lost: %+v", got.Actions[0].Action)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: config("fire", true) + fmt.Sprintf("\nimport {\n to = hue_scene.test\n id = %q\n}\n", id), Check: check("fire")},
		{Config: config("candle", true), Check: check("candle")},
		{Config: config("keep", false), Check: check("candle")},
	}})
}
