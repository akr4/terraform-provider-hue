package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func gradientValue(mode string, points ...hue.XY) types.Object {
	values := []attr.Value{}
	for _, p := range points {
		values = append(values, xyValue(p))
	}
	return types.ObjectValueMust(gradientTypes, map[string]attr.Value{"points": types.ListValueMust(types.ObjectType{AttrTypes: xyTypes}, values), "mode": types.StringValue(mode)})
}
func dynamicsValue(duration int64) types.Object {
	return types.ObjectValueMust(dynamicsTypes, map[string]attr.Value{"duration": types.Int64Value(duration)})
}
func effectsV2Value(t *testing.T, raw string) types.Object {
	t.Helper()
	v, err := effectsV2FromJSON([]byte(raw))
	if err != nil || v.IsNull() {
		t.Fatalf("read %s: %v", raw, err)
	}
	return v
}

// errorText matches text in an error message, which Terraform wraps at spaces.
func errorText(text string) *regexp.Regexp {
	return regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(text), " ", `\s+`))
}

// stateJSON returns a value as the state JSON holds it.
func stateJSON(t *testing.T, v attr.Value) string {
	t.Helper()
	value, err := stateValue(v)
	if err != nil {
		t.Fatal(err)
	}
	return mustJSON(t, value)
}

// The provider sends the API's form, with xy rounded as the bridge stores it.
func TestActionObjectsJSON(t *testing.T) {
	ctx := context.Background()
	noParameters := types.ObjectValueMust(effectTypes, map[string]attr.Value{"effect": types.StringValue("no_effect"), "parameters": types.ObjectValueMust(effectParameterTypes, map[string]attr.Value{"color_xy": types.ObjectNull(xyTypes), "mirek": types.Int64Null(), "speed": types.Float64Null()})})
	for name, c := range map[string]struct {
		value types.Object
		send  func(context.Context, types.Object) (json.RawMessage, error)
		want  string
	}{
		"gradient":                  {gradientValue("interpolated_palette", hue.XY{X: 0.72345, Y: 0.26}, hue.XY{X: 0.3, Y: 0.4}), gradientJSON, `{"points":[{"color":{"xy":{"x":0.7235,"y":0.26}}},{"color":{"xy":{"x":0.3,"y":0.4}}}],"mode":"interpolated_palette"}`},
		"gradient mode":             {gradientValue("segmented_palette", hue.XY{X: 0.3, Y: 0.4}, hue.XY{X: 0.4, Y: 0.4}), gradientJSON, `{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}},{"color":{"xy":{"x":0.4,"y":0.4}}}],"mode":"segmented_palette"}`},
		"effect":                    {effectsV2Value(t, `{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.30004,"y":0.13}},"speed":0.4}}}`), effectsV2JSON, `{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}}`},
		"effect without parameters": {noParameters, effectsV2JSON, `{"action":{"effect":"no_effect"}}`},
		"dynamics":                  {dynamicsValue(800), dynamicsJSON, `{"duration":800}`},
		"no transition":             {dynamicsValue(0), dynamicsJSON, `{"duration":0}`},
	} {
		got, err := c.send(ctx, c.value)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: sent %s, %v; want %s", name, got, err, c.want)
		}
	}
}

// What the bridge returns is read into the attribute, and sent back the same.
func TestActionObjectsFromJSON(t *testing.T) {
	ctx := context.Background()
	points := `"points":[{"color":{"xy":{"x":0.3,"y":0.4}}},{"color":{"xy":{"x":0.4,"y":0.4}}}]`
	for name, c := range map[string]struct {
		read      func([]byte) (types.Object, error)
		send      func(context.Context, types.Object) (json.RawMessage, error)
		raw, sent string
	}{
		"gradient": {gradientFromJSON, gradientJSON, `{` + points + `,"mode":"random_pixelated"}`, `{` + points + `,"mode":"random_pixelated"}`},
		// The API defaults the mode.
		"gradient without mode": {gradientFromJSON, gradientJSON, `{` + points + `}`, `{` + points + `,"mode":"interpolated_palette"}`},
		"effect":                {effectsV2FromJSON, effectsV2JSON, `{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`, `{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`},
		"empty parameters":      {effectsV2FromJSON, effectsV2JSON, `{"action":{"effect":"fire","parameters":{}}}`, `{"action":{"effect":"fire"}}`},
		"dynamics":              {dynamicsFromJSON, dynamicsJSON, `{"duration":400}`, `{"duration":400}`},
	} {
		value, err := c.read([]byte(c.raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := c.send(ctx, value); err != nil || string(got) != c.sent {
			t.Errorf("%s: read %s and sent %s, %v; want %s", name, c.raw, got, err, c.sent)
		}
	}
	// Without what the API requires, there is nothing to manage.
	for name, c := range map[string]struct {
		read func([]byte) (types.Object, error)
		raw  string
	}{
		"no gradient":           {gradientFromJSON, ``},
		"null gradient":         {gradientFromJSON, `null`},
		"effect without action": {effectsV2FromJSON, `{}`},
		"effect without name":   {effectsV2FromJSON, `{"action":{"parameters":{"speed":0.4}}}`},
		"no duration":           {dynamicsFromJSON, `{}`},
	} {
		if value, err := c.read([]byte(c.raw)); err != nil || !value.IsNull() {
			t.Errorf("%s: read %v, %v", name, value, err)
		}
	}
}

func TestReconcileActionObjects(t *testing.T) {
	ctx := context.Background()
	prior := emptyAction()
	prior.Gradient = gradientValue("interpolated_palette", hue.XY{X: 0.72345, Y: 0.26}, hue.XY{X: 0.3, Y: 0.4})
	prior.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370}}}}`)
	prior.Dynamics = dynamicsValue(800)
	prior.Effects = types.StringValue(`{ "effect": "candle" }`)
	points := `"points":[{"color":{"xy":{"x":0.7235,"y":0.26}}},{"color":{"xy":{"x":0.3,"y":0.4}}}]`
	stored := hue.Action{
		Gradient:  json.RawMessage(`{"mode":"interpolated_palette",` + points + `}`),
		EffectsV2: json.RawMessage(`{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370}}}}`),
		Dynamics:  json.RawMessage(`{"duration":800}`),
		Effects:   json.RawMessage(`{"effect":"candle"}`),
	}
	kept := func(name string, got actionModel) {
		t.Helper()
		if !got.Gradient.Equal(prior.Gradient) || !got.EffectsV2.Equal(prior.EffectsV2) || !got.Dynamics.Equal(prior.Dynamics) || !got.Effects.Equal(prior.Effects) {
			t.Errorf("%s: read %+v", name, got)
		}
	}
	kept("stored as sent", reconcile(t, prior, stored))
	// Fields that the provider does not model make no difference.
	extra := hue.Action{
		Gradient:  json.RawMessage(`{"mode":"interpolated_palette",` + points + `,"pixel_count":7}`),
		EffectsV2: json.RawMessage(`{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"intensity":1}},"status":"candle"}`),
		Dynamics:  json.RawMessage(`{"duration":800,"speed":0.5}`),
		Effects:   stored.Effects,
	}
	kept("with other fields", reconcile(t, prior, extra))

	// Changes made in the Hue app are read.
	changed := reconcile(t, prior, hue.Action{
		Gradient:  json.RawMessage(`{"mode":"segmented_palette",` + points + `}`),
		EffectsV2: json.RawMessage(`{"action":{"effect":"fire"}}`),
		Dynamics:  json.RawMessage(`{"duration":1200}`),
	})
	for name, c := range map[string]struct {
		got  attr.Value
		want string
	}{
		"gradient":   {changed.Gradient, `{"mode":"segmented_palette","points":[{"x":0.7235,"y":0.26},{"x":0.3,"y":0.4}]}`},
		"effects_v2": {changed.EffectsV2, `{"effect":"fire","parameters":null}`},
		"dynamics":   {changed.Dynamics, `{"duration":1200}`},
		"effects":    {changed.Effects, `null`},
	} {
		if got := stateJSON(t, c.got); got != c.want {
			t.Errorf("%s read as %s, want %s", name, got, c.want)
		}
	}
	removed := reconcile(t, prior, hue.Action{})
	if !removed.Gradient.IsNull() || !removed.EffectsV2.IsNull() || !removed.Dynamics.IsNull() || !removed.Effects.IsNull() {
		t.Errorf("removed values read as %+v", removed)
	}
	for _, a := range []hue.Action{{Gradient: json.RawMessage(`{"points":"red"}`)}, {EffectsV2: json.RawMessage(`{"action":"fire"}`)}, {Dynamics: json.RawMessage(`{"duration":"fast"}`)}} {
		if _, err := reconcileAction(ctx, prior, a); err == nil {
			t.Errorf("read %+v without an error", a)
		}
	}
}

func TestPlanActionKeepsOmittedValues(t *testing.T) {
	prior := emptyAction()
	prior.Gradient = gradientValue("segmented_palette", hue.XY{X: 0.3, Y: 0.4}, hue.XY{X: 0.4, Y: 0.4})
	prior.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"fire"}}`)
	prior.Dynamics = dynamicsValue(800)
	prior.Effects = types.StringValue(`{"effect":"fire"}`)
	planned := planAction(emptyAction(), emptyAction(), prior)
	if !planned.Gradient.Equal(prior.Gradient) || !planned.EffectsV2.Equal(prior.EffectsV2) || !planned.Dynamics.Equal(prior.Dynamics) || !planned.Effects.Equal(prior.Effects) {
		t.Fatalf("omitted values planned as %+v", planned)
	}
	config := emptyAction()
	config.Dynamics = dynamicsValue(0)
	planned = planAction(config, config, prior)
	if !planned.Dynamics.Equal(config.Dynamics) || !planned.Gradient.Equal(prior.Gradient) {
		t.Fatalf("planned %+v", planned)
	}
}

func TestValidateActionObjects(t *testing.T) {
	ctx := context.Background()
	p := path.Root("actions").AtMapKey("light")
	point := hue.XY{X: 0.3, Y: 0.4}
	// errorAt returns where the only error points.
	errorAt := func(diags diag.Diagnostics) path.Path {
		if len(diags) == 1 {
			if d, ok := diags[0].(diag.DiagnosticWithPath); ok {
				return d.Path()
			}
		}
		return path.Empty()
	}
	gradient := p.AtName("gradient")
	parameters := p.AtName("effects_v2").AtName("parameters")
	for name, c := range map[string]struct {
		change func(*actionModel)
		at     path.Path
	}{
		"one point": {func(a *actionModel) { a.Gradient = gradientValue("interpolated_palette", point) }, gradient.AtName("points")},
		"six points": {func(a *actionModel) {
			a.Gradient = gradientValue("interpolated_palette", point, point, point, point, point, point)
		}, gradient.AtName("points")},
		"point outside": {func(a *actionModel) {
			a.Gradient = gradientValue("interpolated_palette", point, hue.XY{X: 0.8, Y: 0.8})
		}, gradient.AtName("points").AtListIndex(1)},
		"unknown mode":   {func(a *actionModel) { a.Gradient = gradientValue("rainbow", point, point) }, gradient.AtName("mode")},
		"unknown effect": {func(a *actionModel) { a.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"disco"}}`) }, p.AtName("effects_v2").AtName("effect")},
		"effect speed": {func(a *actionModel) {
			a.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"sparkle","parameters":{"speed":2}}}`)
		}, parameters.AtName("speed")},
		"effect color": {func(a *actionModel) {
			a.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":-1,"y":0.3}}}}}`)
		}, parameters.AtName("color_xy")},
		"effect mirek": {func(a *actionModel) {
			a.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"sparkle","parameters":{"color_temperature":{"mirek":2000}}}}`)
		}, parameters.AtName("mirek")},
		"negative duration": {func(a *actionModel) { a.Dynamics = dynamicsValue(-1) }, p.AtName("dynamics").AtName("duration")},
	} {
		a := emptyAction()
		c.change(&a)
		if diags := validateActionObjects(ctx, p, a); !errorAt(diags).Equal(c.at) {
			t.Errorf("%s: %v", name, diags)
		}
	}
	for _, mode := range gradientModes {
		for _, n := range []int{2, 5} {
			a := emptyAction()
			points := make([]hue.XY, n)
			for i := range points {
				points[i] = point
			}
			a.Gradient = gradientValue(mode, points...)
			a.Dynamics = dynamicsValue(0)
			if diags := validateActionObjects(ctx, p, a); diags.HasError() {
				t.Errorf("%s with %d points: %v", mode, n, diags)
			}
		}
	}
	for _, effect := range effectNames {
		a := emptyAction()
		a.EffectsV2 = effectsV2Value(t, fmt.Sprintf(`{"action":{"effect":%q,"parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"color_temperature":{"mirek":250},"speed":1}}}`, effect))
		if diags := validateActionObjects(ctx, p, a); diags.HasError() {
			t.Errorf("%s: %v", effect, diags)
		}
	}
	// A point unknown until apply does not stop checking the others, and
	// points unknown until apply are not counted.
	a := emptyAction()
	a.Gradient = types.ObjectValueMust(gradientTypes, map[string]attr.Value{
		"points": types.ListValueMust(types.ObjectType{AttrTypes: xyTypes}, []attr.Value{xyValue(point), types.ObjectUnknown(xyTypes), xyValue(hue.XY{X: 0.8, Y: 0.8})}),
		"mode":   types.StringUnknown(),
	})
	if diags := validateActionObjects(ctx, p, a); !errorAt(diags).Equal(gradient.AtName("points").AtListIndex(2)) {
		t.Errorf("with an unknown point: %v", diags)
	}
	a.Gradient = types.ObjectValueMust(gradientTypes, map[string]attr.Value{"points": types.ListUnknown(types.ObjectType{AttrTypes: xyTypes}), "mode": types.StringValue("interpolated_palette")})
	a.EffectsV2 = types.ObjectUnknown(effectTypes)
	a.Dynamics = types.ObjectUnknown(dynamicsTypes)
	if diags := validateActionObjects(ctx, p, a); diags.HasError() {
		t.Errorf("with values unknown until apply: %v", diags)
	}
}

func TestSceneEffectV2ColorConflicts(t *testing.T) {
	candle := effectsV2Value(t, `{"action":{"effect":"candle"}}`)
	off := effectsV2Value(t, `{"action":{"effect":"no_effect"}}`)
	for _, tc := range []struct {
		name string
		set  func(*actionModel)
	}{
		{"mirek", func(a *actionModel) { a.Mirek = types.Int64Value(370) }},
		{"xy", func(a *actionModel) { a.XY = xyValue(hue.XY{X: 0.3, Y: 0.4}) }},
		{"gradient", func(a *actionModel) {
			a.Gradient = gradientValue("interpolated_palette", hue.XY{X: 0.3, Y: 0.4}, hue.XY{X: 0.4, Y: 0.4})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := emptyAction()
			a.EffectsV2 = candle
			tc.set(&a)
			if len(validateAction(a)) == 0 {
				t.Fatal("accepted effect with conflicting color")
			}
			// no_effect stops an effect, so it is not checked.
			a.EffectsV2 = off
			if errs := validateAction(a); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
	a := emptyAction()
	a.On = types.BoolValue(true)
	a.Brightness = types.Float64Value(25)
	a.Dynamics = dynamicsValue(800)
	a.EffectsV2 = effectsV2Value(t, `{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`)
	if errs := validateAction(a); len(errs) != 0 {
		t.Fatal(errs)
	}
}

// The deprecated effects stays a JSON string.
func TestSceneEffectsJSON(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `42`, `invalid`} {
		a := emptyAction()
		a.Effects = types.StringValue(raw)
		if len(validateAction(a)) == 0 {
			t.Errorf("accepted %s", raw)
		}
	}
	prior := emptyAction()
	prior.Effects = types.StringValue(`{ "effect": "fire" }`)
	if next := reconcile(t, prior, hue.Action{Effects: json.RawMessage(`{"effect":"fire"}`)}); !next.Effects.Equal(prior.Effects) {
		t.Fatal("equivalent JSON changed representation")
	}
	if next := reconcile(t, prior, hue.Action{Effects: json.RawMessage(`{"effect":"candle"}`)}); next.Effects.Equal(prior.Effects) {
		t.Fatal("remote drift not read")
	}
}

func TestSceneJSONImportCanonicalization(t *testing.T) {
	raw := []byte(`{ "z": 1.0, "a": [9007199254740993, 0.40, null, false] }`)
	got := sceneJSONValue(types.StringNull(), raw)
	want := `{"a":[9007199254740993,0.4,null,false],"z":1}`
	if got.ValueString() != want {
		t.Fatalf("got %s, want %s", got.ValueString(), want)
	}
	prior := types.StringValue(string(raw))
	if !sceneJSONValue(prior, []byte(want)).Equal(prior) {
		t.Fatal("existing user formatting was changed")
	}
	changed := []byte(`{"a":[9007199254740994,0.4,null,false],"z":1}`)
	if sceneJSONValue(prior, changed).Equal(prior) {
		t.Fatal("real drift was hidden")
	}
}

func TestAccSceneActionObjects(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	const sceneID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const groupID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	b.Put("zone", groupID, hue.Group{ID: groupID, Type: "zone", Metadata: hue.Metadata{Name: "Zone", Archetype: "other"}, Children: []hue.Reference{{RID: fakebridge.LightID, RType: "light"}, {RID: fakebridge.WhiteLightID, RType: "light"}}})
	// A scene made in the Hue app, with a mode that is not the default.
	app := map[string]hue.Action{
		fakebridge.LightID:      {On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 40}, Gradient: json.RawMessage(`{"points":[{"color":{"xy":{"x":0.6,"y":0.3}}},{"color":{"xy":{"x":0.2,"y":0.6}}},{"color":{"xy":{"x":0.15,"y":0.1}}}],"mode":"segmented_palette"}`), Dynamics: json.RawMessage(`{"duration":400}`)},
		fakebridge.WhiteLightID: {On: &hue.On{On: true}, EffectsV2: json.RawMessage(`{"action":{"effect":"fire","parameters":{"speed":0.7}}}`), Effects: json.RawMessage(`{"effect":"fire"}`)},
	}
	speed, dynamic := 0.5, false
	b.Put("scene", sceneID, hue.Scene{ID: sceneID, Type: "scene", Metadata: hue.Metadata{Name: "Objects"}, Group: hue.Reference{RID: groupID, RType: "zone"}, Speed: &speed, AutoDynamic: &dynamic, Actions: []hue.SceneAction{
		{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: app[fakebridge.LightID]},
		{Target: hue.Reference{RID: fakebridge.WhiteLightID, RType: "light"}, Action: app[fakebridge.WhiteLightID]},
	}})
	config := func(brightness int, light, white string) string {
		return accProvider + fmt.Sprintf(`
resource "hue_scene" "test" {
 name = "Objects"
 group = %q
 actions = {
  %q = { on = true, brightness = %d%s }
  %q = { on = true%s }
 }
}
`, groupID, fakebridge.LightID, brightness, light, fakebridge.WhiteLightID, white)
	}
	omitted := config(40, "", "")
	written := config(40, `
   gradient = { points = [{ x = 0.72345, y = 0.26 }, { x = 0.17, y = 0.7 }] }
   dynamics = { duration = 800 }`, `
   effects_v2 = { effect = "candle", parameters = { mirek = 370, speed = 0.4 } }
   effects = jsonencode({ effect = "candle" })`)
	writtenStored := map[string]hue.Action{
		fakebridge.LightID:      {Gradient: json.RawMessage(`{"points":[{"color":{"xy":{"x":0.7235,"y":0.26}}},{"color":{"xy":{"x":0.17,"y":0.7}}}],"mode":"interpolated_palette"}`), Dynamics: json.RawMessage(`{"duration":800}`)},
		fakebridge.WhiteLightID: {EffectsV2: json.RawMessage(`{"action":{"effect":"candle","parameters":{"color_temperature":{"mirek":370},"speed":0.4}}}`), Effects: json.RawMessage(`{"effect":"candle"}`)},
	}
	// stored checks the objects that the bridge holds for each light.
	stored := func(want map[string]hue.Action) resource.TestCheckFunc {
		return func(*terraform.State) error {
			scene, err := b.ReadScene(context.Background(), sceneID)
			if err != nil {
				return err
			}
			for _, a := range scene.Actions {
				w := want[a.Target.RID]
				for name, pair := range map[string][2]json.RawMessage{"gradient": {a.Action.Gradient, w.Gradient}, "effects_v2": {a.Action.EffectsV2, w.EffectsV2}, "dynamics": {a.Action.Dynamics, w.Dynamics}, "effects": {a.Action.Effects, w.Effects}} {
					if (len(pair[0]) == 0) != (len(pair[1]) == 0) || (len(pair[0]) > 0 && !sameJSON(pair[0], pair[1])) {
						return fmt.Errorf("%s of %s stored as %s, want %s", name, a.Target.RID, pair[0], pair[1])
					}
				}
			}
			return nil
		}
	}
	light := "hue_scene.test"
	at := func(id, attribute string) string { return "actions." + id + "." + attribute }
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		// The JSON strings that these took before version 0.2.0 are type errors.
		{Config: config(40, `, gradient = jsonencode({ points = [] })`, ""), ExpectError: errorText(`attribute "gradient": object required`)},
		{Config: config(40, "", `, effects_v2 = jsonencode({ action = { effect = "fire" } })`), ExpectError: errorText(`attribute "effects_v2": object required`)},
		{Config: config(40, `, dynamics = jsonencode({ duration = 800 })`, ""), ExpectError: errorText(`attribute "dynamics": object required`)},
		// The limits in the API reference are checked at plan.
		{Config: config(40, `, gradient = { points = [{ x = 0.3, y = 0.4 }] }`, ""), ExpectError: errorText(`gradient.points must have 2 to 5 points, not 1`)},
		{Config: config(40, "", `, effects_v2 = { effect = "disco" }`), ExpectError: errorText(`effect "disco" is not one of`)},
		{Config: config(40, `, dynamics = { duration = -1 }`, ""), ExpectError: errorText(`dynamics.duration must be 0 or more`)},
		// Importing a scene and leaving the objects out keeps them, including
		// a mode that is not the default.
		{Config: omitted + fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", light, sceneID), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(light, plancheck.ResourceActionNoop)}}, Check: resource.ComposeAggregateTestCheckFunc(
			stored(app),
			resource.TestCheckResourceAttr(light, at(fakebridge.LightID, "gradient.mode"), "segmented_palette"),
			resource.TestCheckResourceAttr(light, at(fakebridge.LightID, "gradient.points.#"), "3"),
			resource.TestCheckResourceAttr(light, at(fakebridge.WhiteLightID, "effects_v2.parameters.speed"), "0.7"),
		)},
		{Config: omitted, PlanOnly: true},
		// An update for another reason sends them back as they were.
		{Config: config(41, "", ""), Check: stored(app)},
		// Written values are sent in the API's form and kept in state as written.
		{Config: written, Check: resource.ComposeAggregateTestCheckFunc(
			stored(writtenStored),
			resource.TestCheckResourceAttr(light, at(fakebridge.LightID, "gradient.points.0.x"), "0.72345"),
			resource.TestCheckResourceAttr(light, at(fakebridge.LightID, "gradient.mode"), "interpolated_palette"),
			resource.TestCheckResourceAttr(light, at(fakebridge.LightID, "dynamics.duration"), "800"),
			resource.TestCheckResourceAttr(light, at(fakebridge.WhiteLightID, "effects_v2.effect"), "candle"),
		)},
		{Config: written, PlanOnly: true},
		// Changes made in the Hue app are planned back.
		{PreConfig: func() {
			scene, err := b.ReadScene(context.Background(), sceneID)
			if err != nil {
				t.Fatal(err)
			}
			for i := range scene.Actions {
				a := &scene.Actions[i].Action
				if a.Gradient != nil {
					a.Gradient = json.RawMessage(`{"points":[{"color":{"xy":{"x":0.7235,"y":0.26}}},{"color":{"xy":{"x":0.17,"y":0.7}}}],"mode":"random_pixelated"}`)
					a.Dynamics = json.RawMessage(`{"duration":1200}`)
				} else {
					a.EffectsV2 = json.RawMessage(`{"action":{"effect":"fire"}}`)
				}
			}
			b.Put("scene", sceneID, scene)
		}, Config: written, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(light, plancheck.ResourceActionUpdate)}}, Check: stored(writtenStored)},
		// no_effect stops an effect.
		{Config: config(40, "", `, effects_v2 = { effect = "no_effect" }`), Check: resource.ComposeAggregateTestCheckFunc(
			stored(map[string]hue.Action{fakebridge.LightID: writtenStored[fakebridge.LightID], fakebridge.WhiteLightID: {EffectsV2: json.RawMessage(`{"action":{"effect":"no_effect"}}`), Effects: json.RawMessage(`{"effect":"candle"}`)}}),
		)},
		{Config: config(40, "", `, effects_v2 = { effect = "no_effect" }`), PlanOnly: true},
	}})
}
