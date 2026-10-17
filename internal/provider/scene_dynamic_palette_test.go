package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func nullList(elem map[string]attr.Type) types.List {
	return types.ListNull(types.ObjectType{AttrTypes: elem})
}

func TestDynamicPaletteJSON(t *testing.T) {
	ctx := context.Background()
	raw := `{"color":[{"color":{"xy":{"x":0.6,"y":0.35}},"dimming":{"brightness":33.33}}],"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}}],"dimming":[],"effects":[],"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}}]}`
	palette, err := dynamicPaletteFromJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, err := objectAs[dynamicPaletteModel](ctx, palette)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Dimming.IsNull() || len(m.Color.Elements()) != 1 || len(m.ColorTemperature.Elements()) != 1 || len(m.Effects.Elements()) != 1 {
		t.Fatalf("read %v", palette)
	}
	if !dynamicPaletteEqual(ctx, palette, []byte(raw)) {
		t.Fatal("a palette read from the bridge differs from it")
	}
	// dynamic_palette does not model the deprecated effects list.
	if !dynamicPaletteEqual(ctx, palette, []byte(strings.Replace(raw, `"effects":[]`, `"effects":[{"effect":"sparkle"}]`, 1))) {
		t.Fatal("the deprecated effects list makes a difference")
	}
	if dynamicPaletteEqual(ctx, palette, []byte(strings.Replace(raw, `"speed":0.4`, `"speed":0.5`, 1))) {
		t.Fatal("a different effect speed makes no difference")
	}
	sent, err := dynamicPaletteJSON(ctx, palette)
	if err != nil || !palettesEqual(sent, []byte(raw)) {
		t.Fatalf("sent %s, %v", sent, err)
	}
	for _, empty := range []string{"", "null", `{"color":[],"color_temperature":[],"dimming":[],"effects":[],"effects_v2":[]}`} {
		if p, err := dynamicPaletteFromJSON([]byte(empty)); err != nil || !p.IsNull() {
			t.Errorf("%q read as %v, %v", empty, p, err)
		}
	}
	// Empty effect parameters are left out, and either form read back equals
	// them.
	emptyParameters, err := dynamicPaletteFromJSON([]byte(`{"effects_v2":[{"action":{"effect":"fire","parameters":{}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if sent, _ := dynamicPaletteJSON(ctx, emptyParameters); strings.Contains(string(sent), "parameters") {
		t.Errorf("sent %s", sent)
	}
	for _, stored := range []string{`{"effects_v2":[{"action":{"effect":"fire"}}]}`, `{"effects_v2":[{"action":{"effect":"fire","parameters":{}}}]}`} {
		if !dynamicPaletteEqual(ctx, emptyParameters, []byte(stored)) {
			t.Errorf("parameters = {} differs from %s", stored)
		}
	}
	// A scene that the bridge returns without a palette has an empty one.
	cleared := types.ObjectValueMust(dynamicPaletteTypes, map[string]attr.Value{
		"color":             types.ListValueMust(types.ObjectType{AttrTypes: paletteColorTypes}, []attr.Value{}),
		"color_temperature": nullList(paletteTemperatureTypes), "dimming": nullList(paletteDimmingTypes), "effects": nullList(effectTypes),
	})
	if !dynamicPaletteEqual(ctx, cleared, nil) {
		t.Error("color = [] differs from no palette")
	}
	if dynamicPaletteEqual(ctx, palette, nil) {
		t.Error("a palette with colors equals no palette")
	}
	// Numbers are rounded as the bridge stores them.
	rounded, err := dynamicPaletteFromJSON([]byte(`{"color":[{"color":{"xy":{"x":0.72345,"y":0.26}},"dimming":{"brightness":49.80392156862745}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if sent, _ := dynamicPaletteJSON(ctx, rounded); !strings.Contains(string(sent), `"x":0.7235`) || !strings.Contains(string(sent), `"brightness":49.8}`) {
		t.Fatalf("sent %s", sent)
	}
}

func TestValidateDynamicPalette(t *testing.T) {
	ctx := context.Background()
	read := func(raw string) types.Object {
		p, err := dynamicPaletteFromJSON([]byte(raw))
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
	root := path.Root("dynamic_palette")
	effect := root.AtName("effects").AtListIndex(0)
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
		"four effects":     {`{"effects_v2":[{"action":{"effect":"fire"}},{"action":{"effect":"fire"}},{"action":{"effect":"fire"}},{"action":{"effect":"fire"}}]}`, root.AtName("effects")},
		"bad effect xy":    {`{"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":-1,"y":0.3}}}}}]}`, effect.AtName("parameters").AtName("color_xy")},
		"bad effect mirek": {`{"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color_temperature":{"mirek":2000}}}}]}`, effect.AtName("parameters").AtName("mirek")},
	} {
		if diags := validateDynamicPalette(ctx, read(c.raw)); !errorAt(diags).Equal(c.at) {
			t.Errorf("%s: %v", name, diags)
		}
	}
	// An element unknown until apply does not stop checking the others.
	partial := types.ObjectValueMust(dynamicPaletteTypes, map[string]attr.Value{
		"color": types.ListValueMust(types.ObjectType{AttrTypes: paletteColorTypes}, []attr.Value{
			types.ObjectUnknown(paletteColorTypes),
			types.ObjectValueMust(paletteColorTypes, map[string]attr.Value{"color_xy": xyValue(hue.XY{X: 0.3, Y: 0.3}), "brightness": types.Float64Value(500)}),
		}),
		"color_temperature": nullList(paletteTemperatureTypes), "dimming": nullList(paletteDimmingTypes), "effects": nullList(effectTypes),
	})
	if diags := validateDynamicPalette(ctx, partial); !errorAt(diags).Equal(root.AtName("color").AtListIndex(1).AtName("brightness")) {
		t.Errorf("with an unknown color: %v", diags)
	}
	// The bridge accepts an effect with both a color and a color temperature.
	if diags := validateDynamicPalette(ctx, read(`{"color":[`+color+`],"effects_v2":[{"action":{"effect":"candle"}},{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"color_temperature":{"mirek":250}}}}]}`)); diags.HasError() {
		t.Fatal(diags)
	}
}

func TestPlanPalettes(t *testing.T) {
	const raw = `{"dimming":[{"brightness":50}]}`
	dynamic, err := dynamicPaletteFromJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	unknown := types.ObjectUnknown(dynamicPaletteTypes)
	for name, c := range map[string]struct {
		config, prior  sceneModel
		palette        types.String
		dynamicPalette types.Object
		dropOnChange   bool
	}{
		"moved to dynamic_palette": {
			config:  sceneModel{DynamicPalette: dynamic},
			prior:   sceneModel{Palette: types.StringValue(raw), DynamicPalette: dynamic},
			palette: types.StringValue(raw), dynamicPalette: dynamic, dropOnChange: true,
		},
		"dynamic_palette only": {
			config:  sceneModel{DynamicPalette: dynamic},
			prior:   sceneModel{Palette: types.StringNull(), DynamicPalette: dynamic},
			palette: types.StringNull(), dynamicPalette: dynamic,
		},
		"same palette": {
			config:  sceneModel{Palette: types.StringValue(raw), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			prior:   sceneModel{Palette: types.StringValue(raw), DynamicPalette: dynamic},
			palette: types.StringValue(raw), dynamicPalette: dynamic,
		},
		"changed palette": {
			config:  sceneModel{Palette: types.StringValue(`{"dimming":[{"brightness":60}]}`), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			prior:   sceneModel{Palette: types.StringValue(raw), DynamicPalette: dynamic},
			palette: types.StringValue(`{"dimming":[{"brightness":60}]}`), dynamicPalette: unknown,
		},
		"neither": {
			config:  sceneModel{Palette: types.StringNull(), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			prior:   sceneModel{Palette: types.StringValue(raw), DynamicPalette: dynamic},
			palette: types.StringValue(raw), dynamicPalette: dynamic,
		},
		// A state saved before dynamic_palette existed and not refreshed since.
		"old state": {
			config:  sceneModel{Palette: types.StringNull(), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			prior:   sceneModel{Palette: types.StringValue(raw), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			palette: types.StringValue(raw), dynamicPalette: unknown,
		},
		"old state with an empty palette": {
			config:  sceneModel{Palette: types.StringNull(), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			prior:   sceneModel{Palette: types.StringValue(`{"color":[],"dimming":[]}`), DynamicPalette: types.ObjectNull(dynamicPaletteTypes)},
			palette: types.StringValue(`{"color":[],"dimming":[]}`), dynamicPalette: types.ObjectNull(dynamicPaletteTypes),
		},
	} {
		// Terraform plans configured values as written and the others as unknown.
		planned := sceneModel{Palette: c.config.Palette, DynamicPalette: c.config.DynamicPalette}
		if planned.Palette.IsNull() {
			planned.Palette = types.StringUnknown()
		}
		if planned.DynamicPalette.IsNull() {
			planned.DynamicPalette = unknown
		}
		drop := planPalettes(c.config, &planned, c.prior)
		if !planned.Palette.Equal(c.palette) || !planned.DynamicPalette.Equal(c.dynamicPalette) || drop != c.dropOnChange {
			t.Errorf("%s: planned palette %v, dynamic_palette %v, drop on change %v", name, planned.Palette, planned.DynamicPalette, drop)
		}
	}
}

func TestPlanUnchanged(t *testing.T) {
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"n": tftypes.Number}}
	object := func(n any) tftypes.Value {
		return tftypes.NewValue(typ, map[string]tftypes.Value{"n": tftypes.NewValue(tftypes.Number, n)})
	}
	// 33.333333 read from a float64, and from configuration or state.
	read := big.NewFloat(33.333333)
	parsed, _, err := big.ParseFloat("33.333333", 10, 512, big.ToNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if read.Cmp(parsed) == 0 {
		t.Fatal("the two forms of 33.333333 are exactly equal, so this test checks nothing")
	}
	if !planUnchanged(object(read), object(parsed)) {
		t.Error("33.333333 changed")
	}
	for name, n := range map[string]any{"another number": big.NewFloat(33.33), "unknown": tftypes.UnknownValue, "null": nil} {
		if planUnchanged(object(n), object(parsed)) {
			t.Errorf("%s unchanged", name)
		}
	}
}

func dynamicPaletteSceneConfig(palette string) string {
	c := sceneConfig("on = true", "Palette", "room")
	if palette != "" {
		c = strings.Replace(c, "actions = {", palette+"\n actions = {", 1)
	}
	return c
}

func TestAccSceneDynamicPalette(t *testing.T) {
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
	written := dynamicPaletteSceneConfig(`dynamic_palette = {
  color = [provider::hue::color_from_hex("#ff8800"), { color_xy = { x = 0.3, y = 0.13 }, brightness = 33.333333 }]
  color_temperature = [{ mirek = provider::hue::mirek_from_kelvin(2700), brightness = 40 }]
  dimming = [{ brightness = 50 }]
  effects = [{ effect = "sparkle", parameters = { color_xy = { x = 0.3, y = 0.13 }, speed = 0.4 } }, { effect = "fire", parameters = {} }]
 }`)
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: dynamicPaletteSceneConfig(`palette = "{}"
 dynamic_palette = { dimming = [{ brightness = 50 }] }`), ExpectError: regexp.MustCompile("cannot both be configured")},
		{Config: written, Check: resource.ComposeAggregateTestCheckFunc(
			stored(`{"color":[{"color":{"xy":{"x":0.601,"y":0.3837}},"dimming":{"brightness":100}},{"color":{"xy":{"x":0.3,"y":0.13}},"dimming":{"brightness":33.33}}],"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}}],"dimming":[{"brightness":50}],"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}},{"action":{"effect":"fire"}}]}`),
			resource.TestCheckNoResourceAttr("hue_scene.test", "palette"),
			resource.TestCheckResourceAttr("hue_scene.test", "dynamic_palette.color.1.brightness", "33.333333"),
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
		// Leaving dynamic_palette out keeps the palette on the bridge.
		{Config: dynamicPaletteSceneConfig(""), Check: resource.TestCheckResourceAttr("hue_scene.test", "dynamic_palette.dimming.0.brightness", "50")},
		{Config: dynamicPaletteSceneConfig(""), PlanOnly: true},
		// Empty lists clear it.
		{Config: dynamicPaletteSceneConfig(`dynamic_palette = { color = [] }`), Check: stored(`{}`)},
		{Config: dynamicPaletteSceneConfig(`dynamic_palette = { color = [] }`), PlanOnly: true},
	}})
}

// Rewriting palette as dynamic_palette with the same numbers plans no changes.
// The next update of the scene leaves palette out of the state.
func TestAccScenePaletteToDynamicPalette(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	// palette sends x as written, and dynamic_palette rounds it to 0.6.
	before := dynamicPaletteSceneConfig(`palette = jsonencode({ color = [{ color = { xy = { x = 0.60001, y = 0.35 } }, dimming = { brightness = 33.333333 } }], color_temperature = [{ color_temperature = { mirek = 370 }, dimming = { brightness = 40 } }], dimming = [] })`)
	after := dynamicPaletteSceneConfig(`dynamic_palette = { color = [{ color_xy = { x = 0.60001, y = 0.35 }, brightness = 33.333333 }], color_temperature = [{ mirek = 370, brightness = 40 }] }`)
	renamed := strings.Replace(after, `name = "Palette"`, `name = "Renamed"`, 1)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: before, Check: resource.TestCheckResourceAttr("hue_scene.test", "dynamic_palette.color.0.brightness", "33.333333")},
		{Config: after, PlanOnly: true},
		{Config: after, Check: resource.TestCheckResourceAttrSet("hue_scene.test", "palette")},
		{Config: renamed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction("hue_scene.test", plancheck.ResourceActionUpdate),
			plancheck.ExpectKnownValue("hue_scene.test", tfjsonpath.New("palette"), knownvalue.Null()),
		}}, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckNoResourceAttr("hue_scene.test", "palette"),
			resource.TestCheckResourceAttr("hue_scene.test", "dynamic_palette.color.0.color_xy.x", "0.60001"),
		)},
		{Config: renamed, PlanOnly: true},
	}})
}
