package provider

import (
	"context"
	"encoding/json"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"testing"
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
	delete(want, "kelvin")
	delete(original, "image_id")
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
			a, _ := json.Marshal(want)
			b, _ := json.Marshal(got)
			if string(a) != string(b) {
				t.Fatalf("migration changed managed values: %s", b)
			}
		})
	}
}

// kelvin is an error from version 3 on. Earlier versions write mirek whenever
// they write kelvin, and the upgrade keeps it.
func TestSceneKelvinStateUpgrade(t *testing.T) {
	ctx := context.Background()
	r := &sceneResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	for _, version := range []int64{0, 1, 2} {
		for name, c := range map[string]struct{ mirek, kelvin any }{
			"both":       {mirek: 370, kelvin: 2703},
			"mirek only": {mirek: 200, kelvin: nil},
			"neither":    {mirek: nil, kelvin: nil},
		} {
			action := map[string]any{"gradient": nil, "effects": nil, "effects_v2": nil, "dynamics": nil, "on": true, "brightness": 25, "mirek": c.mirek, "kelvin": c.kelvin, "color_xy": nil}
			state := map[string]any{"id": "scene", "name": "Evening", "group": "room", "actions": map[string]any{"light": action}, "speed": 0.5, "auto_dynamic": false, "palette": nil}
			raw, _ := json.Marshal(state)
			var resp resource.UpgradeStateResponse
			r.UpgradeState(ctx)[version].StateUpgrader(ctx, resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("version %d, %s: %v", version, name, resp.Diagnostics)
			}
			if _, err := resp.DynamicValue.Unmarshal(schemaResp.Schema.Type().TerraformType(ctx)); err != nil {
				t.Fatalf("version %d, %s: %v", version, name, err)
			}
			var got map[string]any
			if err := json.Unmarshal(resp.DynamicValue.JSON, &got); err != nil {
				t.Fatal(err)
			}
			delete(action, "kelvin")
			a, _ := json.Marshal(state)
			b, _ := json.Marshal(got)
			if string(a) != string(b) {
				t.Errorf("version %d, %s: upgraded to %s, want %s", version, name, b, a)
			}
		}
	}
}
