package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestSceneStateUpgrade(t *testing.T) {
	original := map[string]any{"id": "scene", "name": "Evening", "group": "room", "actions": map[string]any{"light": map[string]any{"color_hex": "#ff0000", "color_xy": map[string]any{"x": 0.6915, "y": 0.3083}, "brightness": 2, "on": true, "mirek": nil, "kelvin": nil, "gradient": nil, "effects": nil}}, "auto_dynamic": false, "speed": 0.5, "image_id": nil, "palette": "{}"}
	raw, _ := json.Marshal(original)
	r := &sceneResource{}
	var resp resource.UpgradeStateResponse
	r.UpgradeState(context.Background())[0].StateUpgrader(context.Background(), resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Schema.Version != 3 {
		t.Fatal("schema not versioned")
	}
	if _, err := resp.DynamicValue.Unmarshal(schemaResp.Schema.Type().TerraformType(context.Background())); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.DynamicValue.JSON, &got); err != nil {
		t.Fatal(err)
	}
	a := got["actions"].(map[string]any)["light"].(map[string]any)
	if _, ok := a["color_hex"]; ok {
		t.Fatal("hex retained")
	}
	want := original["actions"].(map[string]any)["light"].(map[string]any)
	delete(want, "color_hex")
	delete(original, "image_id")
	// The palette JSON "{}" has no entries.
	original["palette"] = nil
	expected, _ := json.Marshal(original)
	actual, _ := json.Marshal(got)
	if string(expected) != string(actual) {
		t.Fatalf("migration altered other values: %s", actual)
	}
}

func TestSceneImageStateUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name    string
		r       resource.ResourceWithUpgradeState
		version int64
		state   string
	}{
		{"scene-v1", &sceneResource{}, 1, `{"id":"scene","name":"Evening","group":"room","actions":{},"speed":0.5,"auto_dynamic":false,"palette":"{}","image_id":"image"}`},
		{"smart-scene-v0", &smartSceneResource{}, 0, `{"id":"smart","name":"Day","group":"room","week_timeslots":[],"transition_duration":60000,"state":"active","image_id":"image"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resp resource.UpgradeStateResponse
			tc.r.UpgradeState(context.Background())[tc.version].StateUpgrader(context.Background(), resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: []byte(tc.state)}}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var schemaResp resource.SchemaResponse
			tc.r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
			if _, err := resp.DynamicValue.Unmarshal(schemaResp.Schema.Type().TerraformType(context.Background())); err != nil {
				t.Fatal(err)
			}
			var want, got map[string]any
			_ = json.Unmarshal([]byte(tc.state), &want)
			_ = json.Unmarshal(resp.DynamicValue.JSON, &got)
			delete(want, "image_id")
			if _, ok := want["palette"]; ok {
				want["palette"] = nil
			}
			a, _ := json.Marshal(want)
			b, _ := json.Marshal(got)
			if string(a) != string(b) {
				t.Fatalf("migration changed managed values: %s", b)
			}
		})
	}
}

// A state from v0.1 holds the palette as the JSON string that the bridge
// returned, or that the configuration wrote.
func TestScenePaletteStateUpgrade(t *testing.T) {
	ctx := context.Background()
	r := &sceneResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	full := `{"color":[{"color":{"xy":{"x":0.3,"y":0.4}},"dimming":{"brightness":25}}],"color_temperature":[{"color_temperature":{"mirek":370},"dimming":{"brightness":40}}],"dimming":[{"brightness":50}],"effects":[{"effect":"sparkle"}],"effects_v2":[{"action":{"effect":"sparkle","parameters":{"color":{"xy":{"x":0.3,"y":0.13}},"speed":0.4}}},{"action":{"effect":"fire","parameters":{}}}]}`
	want, err := paletteFromJSON([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	none := types.ObjectNull(paletteTypes)
	for name, c := range map[string]struct {
		palette any
		extra   map[string]any
		want    types.Object
	}{
		"full":       {palette: full, want: want},
		"empty":      {palette: `{"color":[],"color_temperature":[],"dimming":[],"effects":[],"effects_v2":[]}`, want: none},
		"null":       {palette: nil, want: none},
		"unreadable": {palette: `{"color":"red"}`, want: none},
		// Development builds of version 2 wrote dynamic_palette.
		"development": {palette: nil, extra: map[string]any{"dynamic_palette": map[string]any{"color": nil, "color_temperature": nil, "dimming": []any{map[string]any{"brightness": 50}}, "effects": nil}}, want: none},
	} {
		action := map[string]any{"gradient": nil, "effects": nil, "effects_v2": nil, "dynamics": `{"duration":800}`, "on": true, "brightness": 25, "mirek": 370, "kelvin": 2703, "color_xy": nil}
		state := map[string]any{"id": "scene", "name": "Evening", "group": "room", "actions": map[string]any{"light": action}, "speed": 0.5, "auto_dynamic": false, "palette": c.palette}
		for k, v := range c.extra {
			state[k] = v
		}
		raw, _ := json.Marshal(state)
		var resp resource.UpgradeStateResponse
		r.UpgradeState(ctx)[2].StateUpgrader(ctx, resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s: %v", name, resp.Diagnostics)
		}
		value, err := resp.DynamicValue.Unmarshal(schemaResp.Schema.Type().TerraformType(ctx))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		upgraded := tfsdk.State{Schema: schemaResp.Schema, Raw: value}
		var palette types.Object
		if d := upgraded.GetAttribute(ctx, path.Root("palette"), &palette); d.HasError() {
			t.Fatalf("%s: %v", name, d)
		}
		// Numbers read from the state carry more precision than float64, so
		// the values are compared as the state holds them.
		gotPalette, _ := stateValue(palette)
		wantPalette, _ := stateValue(c.want)
		if a, b := mustJSON(t, gotPalette), mustJSON(t, wantPalette); a != b {
			t.Errorf("%s: palette upgraded to %s, want %s", name, a, b)
		}
		// The other values stay as they are.
		var got map[string]any
		if err := json.Unmarshal(resp.DynamicValue.JSON, &got); err != nil {
			t.Fatal(err)
		}
		delete(got, "palette")
		delete(state, "palette")
		for k := range c.extra {
			delete(state, k)
		}
		a, _ := json.Marshal(state)
		b, _ := json.Marshal(got)
		if string(a) != string(b) {
			t.Errorf("%s: upgraded to %s", name, b)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
