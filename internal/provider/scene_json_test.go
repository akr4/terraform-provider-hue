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
	old.EffectsV2 = types.StringValue(`{"action":{"effect":"prism","parameters":{"speed":0.4}}}`)
	old.Dynamics = types.StringValue(`{"duration":800}`)
	actual := hue.Action{Gradient: json.RawMessage(`{"mode":"interpolated_palette","points":[]}`), Effects: json.RawMessage(`{"effect":"candle"}`)}
	next := reconcileAction(old, actual, hue.Light{})
	if !next.Gradient.Equal(old.Gradient) || next.Effects.ValueString() != `{"effect":"candle"}` {
		t.Fatal(next)
	}
	planned := planAction(emptyAction(), emptyAction(), old)
	if !planned.Gradient.Equal(old.Gradient) || !planned.Effects.Equal(old.Effects) || !planned.EffectsV2.Equal(old.EffectsV2) || !planned.Dynamics.Equal(old.Dynamics) {
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
	scene := hue.Scene{ID: id, Metadata: hue.Metadata{Name: "JSON actions fire"}, Group: hue.Reference{RID: groupID, RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Effects: json.RawMessage(`{"effect":"fire"}`), EffectsV2: json.RawMessage(`{"action":{"effect":"fire","parameters":{"speed":0.4,"color":{"xy":{"x":0.3,"y":0.4}}}}}`), Dynamics: json.RawMessage(`{"duration":800}`)}}}}
	speed, dynamic := 1.0, false
	scene.Speed, scene.AutoDynamic = &speed, &dynamic
	scene.Palette = json.RawMessage(`{"color":[],"dimming":[],"color_temperature":[]}`)
	b.Put("scene", id, scene)
	config := func(effect string, explicit bool) string {
		extras := ""
		if explicit {
			extras = fmt.Sprintf(`effects = jsonencode({ effect = %q })
 effects_v2 = jsonencode({ action = { effect = %q, parameters = { speed = 0.4, color = { xy = { x = 0.3, y = 0.4 } } } } })
 dynamics = jsonencode({ duration = 800 })`, effect, effect)
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
			if len(got.Actions[0].Action.Gradient) != 0 || !sameJSON(got.Actions[0].Action.Effects, []byte(fmt.Sprintf(`{"effect":%q}`, effect))) {
				return fmt.Errorf("JSON action lost: %+v", got.Actions[0].Action)
			}
			if !sameJSON(got.Actions[0].Action.EffectsV2, []byte(fmt.Sprintf(`{"action":{"effect":%q,"parameters":{"speed":0.4,"color":{"xy":{"x":0.3,"y":0.4}}}}}`, effect))) || !sameJSON(got.Actions[0].Action.Dynamics, []byte(`{"duration":800}`)) {
				return fmt.Errorf("effect v2 parameters or duration lost: %+v", got.Actions[0].Action)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: config("fire", false) + fmt.Sprintf("\nimport {\n to = hue_scene.test\n id = %q\n}\n", id), Check: check("fire")},
		{Config: config("candle", true), Check: check("candle")},
		{Config: config("keep", false), Check: check("candle")},
		{Config: config("keep", false), PlanOnly: true},
		{Config: config("no_effect", true), Check: check("no_effect")},
	}})
}

func TestSceneNewJSONFields(t *testing.T) {
	for _, field := range []string{"effects_v2", "dynamics"} {
		t.Run(field, func(t *testing.T) {
			for _, raw := range []string{`[]`, `null`, `42`, `invalid`} {
				a := emptyAction()
				if field == "effects_v2" {
					a.EffectsV2 = types.StringValue(raw)
				} else {
					a.Dynamics = types.StringValue(raw)
				}
				if len(validateAction(a)) == 0 {
					t.Fatalf("accepted %s", raw)
				}
			}
		})
	}
	prior := emptyAction()
	prior.EffectsV2 = types.StringValue(`{ "action": {"effect":"fire"} }`)
	prior.Dynamics = types.StringValue(`{ "duration": 0 }`)
	actual := hue.Action{EffectsV2: json.RawMessage(`{"action":{"effect":"fire"}}`), Dynamics: json.RawMessage(`{"duration":0}`)}
	next := reconcileAction(prior, actual, hue.Light{})
	if !next.EffectsV2.Equal(prior.EffectsV2) || !next.Dynamics.Equal(prior.Dynamics) {
		t.Fatal("equivalent JSON changed representation")
	}
	actual.Dynamics = json.RawMessage(`{"duration":1200}`)
	actual.EffectsV2 = json.RawMessage(`{"action":{"effect":"candle"}}`)
	next = reconcileAction(prior, actual, hue.Light{})
	if next.Dynamics.Equal(prior.Dynamics) || next.EffectsV2.Equal(prior.EffectsV2) {
		t.Fatal("remote drift not read")
	}
}

func TestSceneEffectV2ColorConflicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*actionModel)
	}{
		{"mirek", func(a *actionModel) { a.Mirek = types.Int64Value(370) }},
		{"kelvin", func(a *actionModel) { a.Kelvin = types.Int64Value(2700) }},
		{"xy", func(a *actionModel) { a.XY = xyValue(hue.XY{X: 0.3, Y: 0.4}) }},
		{"gradient", func(a *actionModel) { a.Gradient = types.StringValue(`{"points":[]}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := emptyAction()
			a.EffectsV2 = types.StringValue(`{"action":{"effect":"candle"}}`)
			tc.set(&a)
			if len(validateAction(a)) == 0 {
				t.Fatal("accepted effect with conflicting color")
			}
		})
	}
	a := emptyAction()
	a.On = types.BoolValue(true)
	a.Brightness = types.Float64Value(25)
	a.Dynamics = types.StringValue(`{"duration":800}`)
	a.EffectsV2 = types.StringValue(`{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`)
	if errs := validateAction(a); len(errs) != 0 {
		t.Fatal(errs)
	}
}
