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
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func nullList(elem map[string]attr.Type) types.List {
	return types.ListNull(types.ObjectType{AttrTypes: elem})
}

func TestPalettesEqual(t *testing.T) {
	a := `{"color":[{"color":{"xy":{"x":0.16,"y":0.1}},"dimming":{"brightness":10}}]}`
	b := `{"color":[{"color":{"xy":{"x":0.16,"y":0.0999999}},"dimming":{"brightness":10}}],"effects":[],"dimming":[]}`
	if !palettesEqual([]byte(a), []byte(b)) {
		t.Fatal("bridge normalization differs")
	}
	if !palettesEqual([]byte(a), []byte(strings.Replace(b, `"dimming":[]`, `"dimming":null`, 1))) {
		t.Fatal("a null category differs")
	}
	if palettesEqual([]byte(a), []byte(strings.ReplaceAll(b, "0.16", "0.20"))) {
		t.Fatal("real drift ignored")
	}
	rounded := `{"color":[{"color":{"xy":{"x":0.16,"y":0.1}},"dimming":{"brightness":33.33}}]}`
	if !palettesEqual([]byte(strings.ReplaceAll(rounded, "33.33", "33.333333")), []byte(rounded)) {
		t.Fatal("brightness rounded by the bridge differs")
	}
	if palettesEqual([]byte(strings.ReplaceAll(rounded, "33.33", "33.34")), []byte(rounded)) {
		t.Fatal("brightness drift ignored")
	}
	if !palettesEqual([]byte(`{"dimming":[{"brightness":1.005}]}`), []byte(`{"dimming":[{"brightness":1}]}`)) {
		t.Fatal("brightness rounded before sending differs")
	}
	for _, raw := range []string{"[]", "null", "bad"} {
		if palettesEqual([]byte(raw), []byte(raw)) {
			t.Fatal("invalid palette equal")
		}
	}
}

func TestPaletteJSON(t *testing.T) {
	ctx := context.Background()
	raw := `{"color":[{"color":{"xy":{"x":0.6,"y":0.35}},"dimming":{"brightness":33.33}}],"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}}],"dimming":[],"effects":[],"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}}]}`
	palette, err := paletteFromJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, err := objectAs[paletteModel](ctx, palette)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Dimming.IsNull() || len(m.Color.Elements()) != 1 || len(m.ColorTemperature.Elements()) != 1 || len(m.EffectsV2.Elements()) != 1 {
		t.Fatalf("read %v", palette)
	}
	if !paletteEqual(ctx, palette, []byte(raw)) {
		t.Fatal("a palette read from the bridge differs from it")
	}
	// palette does not model the deprecated effects list.
	if !paletteEqual(ctx, palette, []byte(strings.Replace(raw, `"effects":[]`, `"effects":[{"effect":"sparkle"}]`, 1))) {
		t.Fatal("the deprecated effects list makes a difference")
	}
	// A category that comes back as null is empty.
	if !paletteEqual(ctx, palette, []byte(strings.Replace(raw, `"dimming":[]`, `"dimming":null`, 1))) {
		t.Fatal("a null category makes a difference")
	}
	if paletteEqual(ctx, palette, []byte(strings.Replace(raw, `"speed":0.4`, `"speed":0.5`, 1))) {
		t.Fatal("a different effect speed makes no difference")
	}
	sent, err := paletteJSON(ctx, palette)
	if err != nil || !palettesEqual(sent, []byte(raw)) {
		t.Fatalf("sent %s, %v", sent, err)
	}
	for _, empty := range []string{"", "null", `{"color":[],"color_temperature":[],"dimming":[],"effects":[],"effects_v2":[]}`} {
		if p, err := paletteFromJSON([]byte(empty)); err != nil || !p.IsNull() {
			t.Errorf("%q read as %v, %v", empty, p, err)
		}
	}
	// parameters = {} is not sent, and equals both forms that the bridge may
	// store. Both read back without parameters.
	fire := types.ObjectValueMust(effectTypes, map[string]attr.Value{"effect": types.StringValue("fire"), "parameters": types.ObjectValueMust(effectParameterTypes, map[string]attr.Value{"color_xy": types.ObjectNull(xyTypes), "mirek": types.Int64Null(), "speed": types.Float64Null()})})
	emptyParameters := types.ObjectValueMust(paletteTypes, map[string]attr.Value{
		"color": nullList(paletteColorTypes), "color_temperature": nullList(paletteTemperatureTypes), "dimming": nullList(paletteDimmingTypes),
		"effects_v2": types.ListValueMust(types.ObjectType{AttrTypes: effectTypes}, []attr.Value{fire}),
	})
	if sent, _ := paletteJSON(ctx, emptyParameters); strings.Contains(string(sent), "parameters") {
		t.Errorf("sent %s", sent)
	}
	for _, stored := range []string{`{"effects_v2":[{"action":{"effect":"fire"}}]}`, `{"effects_v2":[{"action":{"effect":"fire","parameters":{}}}]}`} {
		if !paletteEqual(ctx, emptyParameters, []byte(stored)) {
			t.Errorf("parameters = {} differs from %s", stored)
		}
		read, err := paletteFromJSON([]byte(stored))
		if err != nil {
			t.Fatal(err)
		}
		effects, err := listOf[effectModel](ctx, read.Attributes()["effects_v2"].(types.List))
		if err != nil || len(effects) != 1 || !effects[0].Parameters.IsNull() {
			t.Errorf("%s read as %v, %v", stored, read, err)
		}
	}
	// A scene that the bridge returns without a palette has an empty one.
	cleared := types.ObjectValueMust(paletteTypes, map[string]attr.Value{
		"color":             types.ListValueMust(types.ObjectType{AttrTypes: paletteColorTypes}, []attr.Value{}),
		"color_temperature": nullList(paletteTemperatureTypes), "dimming": nullList(paletteDimmingTypes), "effects_v2": nullList(effectTypes),
	})
	if !paletteEqual(ctx, cleared, nil) {
		t.Error("color = [] differs from no palette")
	}
	if paletteEqual(ctx, palette, nil) {
		t.Error("a palette with colors equals no palette")
	}
	// Numbers are rounded as the bridge stores them.
	rounded, err := paletteFromJSON([]byte(`{"color":[{"color":{"xy":{"x":0.72345,"y":0.26}},"dimming":{"brightness":49.80392156862745}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if sent, _ := paletteJSON(ctx, rounded); !strings.Contains(string(sent), `"x":0.7235`) || !strings.Contains(string(sent), `"brightness":49.8}`) {
		t.Fatalf("sent %s", sent)
	}
}

func TestValidatePalette(t *testing.T) {
	ctx := context.Background()
	read := func(raw string) types.Object {
		p, err := paletteFromJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// errorAt returns where the only error points.
	errorAt := func(diags diag.Diagnostics) path.Path {
		if len(diags) == 1 {
			if d, ok := diags[0].(diag.DiagnosticWithPath); ok {
				return d.Path()
			}
		}
		return path.Empty()
	}
	root := path.Root("palette")
	effect := root.AtName("effects_v2").AtListIndex(0)
	color := `{"color":{"xy":{"x":0.3,"y":0.3}},"dimming":{"brightness":50}}`
	for name, c := range map[string]struct {
		raw string
		at  path.Path
	}{
		"ten colors":       {`{"color":[` + strings.TrimSuffix(strings.Repeat(color+",", 10), ",") + `]}`, root.AtName("color")},
		"two temperatures": {`{"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}},{"color_temperature":{"mirek":300},"dimming":{"brightness":40}}]}`, root.AtName("color_temperature")},
		"bad mirek":        {`{"color_temperature":[{"color_temperature":{"mirek":20},"dimming":{"brightness":40}}]}`, root.AtName("color_temperature").AtListIndex(0).AtName("mirek")},
		"bad brightness":   {`{"dimming":[{"brightness":101}]}`, root.AtName("dimming").AtListIndex(0).AtName("brightness")},
		"bad xy":           {`{"color":[` + color + `,{"color":{"xy":{"x":0.8,"y":0.8}},"dimming":{"brightness":50}}]}`, root.AtName("color").AtListIndex(1).AtName("color_xy")},
		"unknown effect":   {`{"effects_v2":[{"action":{"effect":"disco"}}]}`, effect.AtName("effect")},
		"bad effect speed": {`{"effects_v2":[{"action":{"effect":"sparkle","parameters":{"speed":2}}}]}`, effect.AtName("parameters").AtName("speed")},
		"four effects":     {`{"effects_v2":[{"action":{"effect":"fire"}},{"action":{"effect":"fire"}},{"action":{"effect":"fire"}},{"action":{"effect":"fire"}}]}`, root.AtName("effects_v2")},
		"bad effect xy":    {`{"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":-1,"y":0.3}}}}}]}`, effect.AtName("parameters").AtName("color_xy")},
		"bad effect mirek": {`{"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color_temperature":{"mirek":2000}}}}]}`, effect.AtName("parameters").AtName("mirek")},
	} {
		if diags := validatePalette(ctx, read(c.raw)); !errorAt(diags).Equal(c.at) {
			t.Errorf("%s: %v", name, diags)
		}
	}
	// An element unknown until apply does not stop checking the others.
	partial := types.ObjectValueMust(paletteTypes, map[string]attr.Value{
		"color": types.ListValueMust(types.ObjectType{AttrTypes: paletteColorTypes}, []attr.Value{
			types.ObjectUnknown(paletteColorTypes),
			types.ObjectValueMust(paletteColorTypes, map[string]attr.Value{"color_xy": xyValue(hue.XY{X: 0.3, Y: 0.3}), "brightness": types.Float64Value(500)}),
		}),
		"color_temperature": nullList(paletteTemperatureTypes), "dimming": nullList(paletteDimmingTypes), "effects_v2": nullList(effectTypes),
	})
	if diags := validatePalette(ctx, partial); !errorAt(diags).Equal(root.AtName("color").AtListIndex(1).AtName("brightness")) {
		t.Errorf("with an unknown color: %v", diags)
	}
	// The bridge accepts an effect with both a color and a color temperature.
	if diags := validatePalette(ctx, read(`{"color":[`+color+`],"effects_v2":[{"action":{"effect":"candle"}},{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"color_temperature":{"mirek":250}}}}]}`)); diags.HasError() {
		t.Fatal(diags)
	}
}

func paletteSceneConfig(palette string) string {
	c := sceneConfig("on = true", "Palette", "room")
	if palette != "" {
		c = strings.Replace(c, "actions = {", palette+"\n actions = {", 1)
	}
	return c
}

func TestAccScenePalette(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	var id string
	stored := func(want string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			id = s.RootModule().Resources["hue_scene.test"].Primary.ID
			scene, err := b.ReadScene(context.Background(), id)
			if err != nil {
				return err
			}
			if !palettesEqual(scene.Palette, []byte(want)) {
				return fmt.Errorf("palette stored as %s, want %s", scene.Palette, want)
			}
			return nil
		}
	}
	// unsent checks that the requests since a step began left the palette out.
	var marked int
	mark := func() { marked = len(b.Requests()) }
	unsent := func(*terraform.State) error {
		for _, r := range b.Requests()[marked:] {
			var payload map[string]json.RawMessage
			if r.Method == "PUT" && json.Unmarshal(r.Body, &payload) == nil && payload["palette"] != nil {
				return fmt.Errorf("omitted palette sent: %s", r.Body)
			}
		}
		return nil
	}
	kept := sceneConfig("on = true", "Kept", "room")
	// writtenStored is what the bridge stores for written.
	const writtenStored = `{"color":[{"color":{"xy":{"x":0.601,"y":0.3837}},"dimming":{"brightness":100}},{"color":{"xy":{"x":0.3,"y":0.13}},"dimming":{"brightness":33.33}}],"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}}],"dimming":[{"brightness":50}],"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}},{"action":{"effect":"fire"}}]}`
	written := paletteSceneConfig(`palette = {
  color = [provider::hue::color_from_hex("#ff8800"), { color_xy = { x = 0.3, y = 0.13 }, brightness = 33.333333 }]
  color_temperature = [{ mirek = provider::hue::mirek_from_kelvin(2700), brightness = 40 }]
  dimming = [{ brightness = 50 }]
  effects_v2 = [{ effect = "sparkle", parameters = { color_xy = { x = 0.3, y = 0.13 }, speed = 0.4 } }, { effect = "fire", parameters = {} }]
 }`)
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		// The JSON string that palette took before version 0.2.0 is a type error.
		{Config: paletteSceneConfig(`palette = jsonencode({ dimming = [{ brightness = 50 }] })`), ExpectError: regexp.MustCompile(`Inappropriate value for attribute "palette"`)},
		{Config: written, Check: resource.ComposeAggregateTestCheckFunc(
			stored(writtenStored),
			resource.TestCheckResourceAttr("hue_scene.test", "palette.color.1.brightness", "33.333333"),
		)},
		{Config: written, PlanOnly: true},
		// The deprecated effects list, which other apps may still write, makes
		// no difference.
		{PreConfig: func() {
			s, err := b.ReadScene(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal(s.Palette, &p); err != nil {
				t.Fatal(err)
			}
			p["effects"] = []any{map[string]any{"effect": "sparkle"}}
			if s.Palette, err = json.Marshal(p); err != nil {
				t.Fatal(err)
			}
			b.Put("scene", id, s)
		}, Config: written, PlanOnly: true},
		// Changes made in the Hue app are planned back.
		{PreConfig: func() {
			s, err := b.ReadScene(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			s.Palette = json.RawMessage(`{"dimming":[{"brightness":10}]}`)
			b.Put("scene", id, s)
		}, Config: written, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("hue_scene.test", plancheck.ResourceActionUpdate)}}},
		// Leaving palette out keeps the palette on the bridge, also when the
		// scene is updated for another reason.
		{PreConfig: mark, Config: kept, Check: resource.ComposeAggregateTestCheckFunc(
			stored(writtenStored), unsent,
			resource.TestCheckResourceAttr("hue_scene.test", "palette.dimming.0.brightness", "50"),
		)},
		{Config: kept, PlanOnly: true},
		// Empty lists clear it.
		{Config: paletteSceneConfig(`palette = { color = [] }`), Check: stored(`{}`)},
		{Config: paletteSceneConfig(`palette = { color = [] }`), PlanOnly: true},
	}})
}
