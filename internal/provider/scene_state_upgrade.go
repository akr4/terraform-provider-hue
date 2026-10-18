package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.ResourceWithUpgradeState = (*sceneResource)(nil)

// Each upgrader converts directly to the current schema without bridge access.
func (r *sceneResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: sceneStateUpgrader(removeSceneHex, removeSceneImage, upgradeScenePalette),
		1: sceneStateUpgrader(removeSceneImage, upgradeScenePalette),
		2: sceneStateUpgrader(upgradeScenePalette),
	}
}

var _ resource.ResourceWithUpgradeState = (*smartSceneResource)(nil)

func (r *smartSceneResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{0: sceneStateUpgrader(removeSceneImage)}
}

// sceneStateUpgrader applies steps to the state JSON in order.
func sceneStateUpgrader(steps ...func([]byte) ([]byte, error)) resource.StateUpgrader {
	return resource.StateUpgrader{StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		if req.RawState == nil {
			resp.Diagnostics.AddError("Scene state upgrade failed", "Missing prior state.")
			return
		}
		data := req.RawState.JSON
		for _, step := range steps {
			var err error
			if data, err = step(data); err != nil {
				resp.Diagnostics.AddError("Scene state upgrade failed", err.Error())
				return
			}
		}
		resp.DynamicValue = &tfprotov6.DynamicValue{JSON: data}
	}}
}

// upgradeScenePalette reads palette, which states before version 3 hold as a
// JSON string, into the object that palette is now, as refresh reads it from
// the bridge. A palette that cannot be read is left null for refresh to read
// from the bridge. It also removes dynamic_palette, which development builds
// of version 2 wrote.
func upgradeScenePalette(data []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("expected scene object")
	}
	delete(root, "dynamic_palette")
	palette := types.ObjectNull(paletteTypes)
	var raw *string
	if json.Unmarshal(root["palette"], &raw) == nil && raw != nil {
		if p, err := paletteFromJSON([]byte(*raw)); err == nil {
			palette = p
		}
	}
	value, err := stateValue(palette)
	if err != nil {
		return nil, err
	}
	if root["palette"], err = json.Marshal(value); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

// stateValue returns a known or null value as the state JSON holds it.
func stateValue(v attr.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}
	if v.IsUnknown() {
		return nil, fmt.Errorf("cannot write an unknown value to state")
	}
	switch v := v.(type) {
	case types.Object:
		m := map[string]any{}
		for k, a := range v.Attributes() {
			value, err := stateValue(a)
			if err != nil {
				return nil, err
			}
			m[k] = value
		}
		return m, nil
	case types.List:
		l := []any{}
		for _, e := range v.Elements() {
			value, err := stateValue(e)
			if err != nil {
				return nil, err
			}
			l = append(l, value)
		}
		return l, nil
	case types.String:
		return v.ValueString(), nil
	case types.Int64:
		return v.ValueInt64(), nil
	case types.Float64:
		return v.ValueFloat64(), nil
	}
	return nil, fmt.Errorf("cannot write %s to state", v.Type(context.Background()))
}

func removeSceneImage(data []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("expected scene object")
	}
	delete(root, "image_id")
	return json.Marshal(root)
}

func removeSceneHex(data []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("expected scene object")
	}
	var actions map[string]map[string]json.RawMessage
	if raw := root["actions"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &actions); err != nil {
			return nil, err
		}
		for _, a := range actions {
			delete(a, "color_hex")
		}
		raw, err := json.Marshal(actions)
		if err != nil {
			return nil, err
		}
		root["actions"] = raw
	}
	return json.Marshal(root)
}
