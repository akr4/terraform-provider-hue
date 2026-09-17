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
	if schemaResp.Schema.Version != 2 {
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
