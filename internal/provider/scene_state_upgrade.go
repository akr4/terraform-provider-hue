package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.ResourceWithUpgradeState = (*sceneResource)(nil)

// Each upgrader converts directly to the current schema without bridge access.
func (r *sceneResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{0: sceneStateUpgrader(true), 1: sceneStateUpgrader(false)}
}

var _ resource.ResourceWithUpgradeState = (*smartSceneResource)(nil)

func (r *smartSceneResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{0: sceneStateUpgrader(false)}
}

func sceneStateUpgrader(removeHex bool) resource.StateUpgrader {
	return resource.StateUpgrader{StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		if req.RawState == nil {
			resp.Diagnostics.AddError("Scene state upgrade failed", "Missing prior state.")
			return
		}
		data := req.RawState.JSON
		var err error
		if removeHex {
			data, err = removeSceneHex(data)
		}
		if err == nil {
			data, err = removeSceneImage(data)
		}
		if err != nil {
			resp.Diagnostics.AddError("Scene state upgrade failed", err.Error())
			return
		}
		resp.DynamicValue = &tfprotov6.DynamicValue{JSON: data}
	}}
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
