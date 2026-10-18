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
	return map[int64]resource.StateUpgrader{
		0: sceneStateUpgrader(removeSceneHex, removeSceneImage, removeSceneKelvin),
		1: sceneStateUpgrader(removeSceneImage, removeSceneKelvin),
		2: sceneStateUpgrader(removeSceneKelvin),
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
	return editSceneActions(data, func(a map[string]json.RawMessage) error {
		delete(a, "color_hex")
		return nil
	})
}

// removeSceneKelvin clears kelvin, which is an error from version 3 on. Earlier
// versions write mirek, read from the bridge, whenever they write kelvin.
func removeSceneKelvin(data []byte) ([]byte, error) {
	return editSceneActions(data, func(a map[string]json.RawMessage) error {
		delete(a, "kelvin")
		return nil
	})
}

// editSceneActions applies edit to each action in the state JSON.
func editSceneActions(data []byte, edit func(map[string]json.RawMessage) error) ([]byte, error) {
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
			if err := edit(a); err != nil {
				return nil, err
			}
		}
		raw, err := json.Marshal(actions)
		if err != nil {
			return nil, err
		}
		root["actions"] = raw
	}
	return json.Marshal(root)
}
