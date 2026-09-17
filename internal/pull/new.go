package pull

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Inventory includes all managed room, zone, scene and behavior instance IDs, including indexed and module instances.
func Inventory(data []byte, scope ...string) (map[string]bool, map[string]string, error) {
	var state struct {
		Resources []struct {
			Mode, Type, Name, Module, Provider string
			Instances                          []struct {
				IndexKey   json.RawMessage `json:"index_key"`
				Attributes struct{ ID string }
			}
		}
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, nil, fmt.Errorf("invalid Terraform state")
	}
	scenes := map[string]bool{}
	groups := map[string]string{}
	for _, r := range state.Resources {
		if r.Mode != "managed" {
			continue
		}
		for _, i := range r.Instances {
			if r.Type == "hue_scene" || r.Type == "hue_smart_scene" || r.Type == "hue_room" || r.Type == "hue_zone" || r.Type == "hue_behavior_instance" {
				scenes[i.Attributes.ID] = true
			}
			if (r.Type == "hue_room" || r.Type == "hue_zone") && r.Module == moduleScope(scope) && len(i.IndexKey) == 0 && r.Provider == `provider["registry.terraform.io/akr4/hue"]` {
				groups[i.Attributes.ID] = r.Type + "." + r.Name
			}
		}
	}
	return scenes, groups, nil
}

// NewScene emits only provider-supported attributes. Unknown action fields fail
// explicitly instead of silently producing an incomplete scene definition.
func NewScene(raw json.RawMessage, name string, groups map[string]string) ([]byte, error) {
	if !hclsyntax.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid resource name")
	}
	var scene hue.Scene
	if err := json.Unmarshal(raw, &scene); err != nil {
		return nil, err
	}
	if scene.ID == "" || scene.Metadata.Name == "" || (scene.Group.RType != "room" && scene.Group.RType != "zone") {
		return nil, fmt.Errorf("invalid scene identity/group")
	}
	var details struct {
		Actions []struct{ Action map[string]json.RawMessage }
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil, err
	}
	for _, a := range details.Actions {
		for key := range a.Action {
			switch key {
			case "on", "dimming", "color", "color_temperature", "gradient", "effects":
			default:
				return nil, fmt.Errorf("scene contains unsupported action field %s; definition was not generated", key)
			}
		}
	}
	f := hclwrite.NewEmptyFile()
	b := f.Body().AppendNewBlock("resource", []string{"hue_scene", name}).Body()
	b.SetAttributeValue("name", cty.StringVal(scene.Metadata.Name))
	if addr := groups[scene.Group.RID]; addr != "" {
		parts := strings.Split(addr, ".")
		b.SetAttributeTraversal("group", hcl.Traversal{hcl.TraverseRoot{Name: parts[0]}, hcl.TraverseAttr{Name: parts[1]}, hcl.TraverseAttr{Name: "id"}})
	} else {
		b.SetAttributeValue("group", cty.StringVal(scene.Group.RID))
	}
	if scene.Speed != nil {
		b.SetAttributeValue("speed", cty.NumberFloatVal(*scene.Speed))
	}
	if scene.AutoDynamic != nil {
		b.SetAttributeValue("auto_dynamic", cty.BoolVal(*scene.AutoDynamic))
	}
	actions := map[string]cty.Value{}
	for _, a := range scene.Actions {
		if a.Target.RType != "light" {
			return nil, fmt.Errorf("unsupported action target")
		}
		if _, exists := actions[a.Target.RID]; exists {
			return nil, fmt.Errorf("duplicate action target")
		}
		fields := map[string]cty.Value{}
		for key, raw := range map[string]json.RawMessage{"gradient": a.Action.Gradient, "effects": a.Action.Effects} {
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			if _, err := configurationValue(raw); err != nil {
				return nil, fmt.Errorf("invalid %s action: %w", key, err)
			}
			fields[key] = cty.StringVal(string(raw))
		}

		if a.Action.On != nil {
			fields["on"] = cty.BoolVal(a.Action.On.On)
		}
		if a.Action.Dimming != nil {
			fields["brightness"] = cty.NumberFloatVal(a.Action.Dimming.Brightness)
		}
		if a.Action.ColorTemperature != nil {
			fields["mirek"] = cty.NumberIntVal(a.Action.ColorTemperature.Mirek)
		}
		if a.Action.Color != nil {
			fields["color_xy"] = cty.ObjectVal(map[string]cty.Value{"x": cty.NumberFloatVal(a.Action.Color.XY.X), "y": cty.NumberFloatVal(a.Action.Color.XY.Y)})
		}
		actions[a.Target.RID] = cty.ObjectVal(fields)
	}
	b.SetAttributeValue("actions", cty.ObjectVal(actions))
	return sceneJSONExpressions(f.Bytes())
}

// NewResourcePath refuses existing declarations and files, including overrides.
func NewResourcePath(dir, kind, name string) (string, error) {
	if !hclsyntax.ValidIdentifier(name) {
		return "", fmt.Errorf("invalid resource name")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tf.json") {
			return "", fmt.Errorf("JSON configuration is not supported")
		}
		if !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		f, d := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
		if d.HasErrors() {
			return "", fmt.Errorf("cannot parse %s", path)
		}
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			if b.Type == "resource" && len(b.Labels) == 2 && b.Labels[0] == "hue_"+kind && b.Labels[1] == name {
				return "", fmt.Errorf("hue_%s.%s already has a definition", kind, name)
			}
		}
	}
	path := filepath.Join(dir, kind+"_"+name+".tf")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return "", fmt.Errorf("destination exists or cannot be inspected: %s", path)
	}
	return path, nil
}

func WriteNew(path string, src []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(src)
	closeErr := f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}
	if closeErr != nil {
		os.Remove(path)
		return closeErr
	}
	return nil
}

func AddressReserved(data []byte, kind, name string, scope ...string) bool {
	var state struct {
		Resources []struct{ Mode, Type, Name, Module string }
	}
	if json.Unmarshal(data, &state) != nil {
		return true
	}
	for _, r := range state.Resources {
		if r.Mode == "managed" && r.Module == moduleScope(scope) && r.Type == "hue_"+kind && r.Name == name {
			return true
		}
	}
	return false
}

func moduleScope(scope []string) string {
	if len(scope) > 0 {
		return scope[0]
	}
	return ""
}

func sceneJSONExpressions(src []byte) ([]byte, error) {
	f, d := hclsyntax.ParseConfig(src, "generated.tf", hcl.InitialPos)
	if d.HasErrors() {
		return nil, fmt.Errorf("invalid generated scene")
	}
	c := &Change{Original: src}
	b := f.Body.(*hclsyntax.Body).Blocks[0]
	actions, err := object(b.Body.Attributes["actions"].Expr)
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		fields, err := object(action)
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"gradient", "effects"} {
			e := fields[key]
			if e == nil {
				continue
			}
			v, d := e.Value(nil)
			if d.HasErrors() {
				return nil, fmt.Errorf("invalid JSON action")
			}
			obj, err := configurationValue([]byte(v.AsString()))
			if err != nil {
				return nil, err
			}
			r := e.Range()
			c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, After: string(hclwrite.TokensForFunctionCall("jsonencode", hclwrite.TokensForValue(obj)).Bytes())})
		}
	}
	c.finish()
	return hclwrite.Format(c.Updated), nil
}
