package pull

import (
	"encoding/json"
	"fmt"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// switchColor replaces the field key and value separately, preserving the
// surrounding action and trailing annotations. It only accepts exclusive modes.
func (c *Change) switchColor(action hclsyntax.Expression, fields map[string]hclsyntax.Expression, oldMirek, oldKelvin *int64, oldXY *hue.XY, remote hue.Action, id string) (bool, error) {
	temperature := "mirek"
	if fields["kelvin"] != nil {
		temperature = "kelvin"
	}
	if fields["color_hex"] != nil {
		return false, fmt.Errorf("%s: color_hex is not supported for pull; use color_xy", id)
	}
	if fields["mirek"] != nil && fields["kelvin"] != nil {
		return false, fmt.Errorf("both mirek and kelvin are configured")
	}
	from, to := "", ""
	value := cty.NilVal
	if fields[temperature] != nil && fields["color_xy"] == nil && remote.Color != nil && remote.ColorTemperature == nil {
		previous := oldMirek
		if temperature == "kelvin" {
			previous = oldKelvin
		}
		if previous == nil || oldXY != nil {
			return false, fmt.Errorf("%s: color mode differs from state; resolve the local edit first", id)
		}
		old := cty.NumberIntVal(*previous)
		if err := c.scalar(fields[temperature], old, old, id, temperature); err != nil {
			return false, err
		}
		xy := remote.Color.XY
		if xy.X < 0 || xy.X > 1 || xy.Y < 0 || xy.Y > 1 {
			return false, fmt.Errorf("invalid bridge color_xy")
		}
		from, to = temperature, "color_xy"
		value = cty.ObjectVal(map[string]cty.Value{"x": cty.NumberFloatVal(xy.X), "y": cty.NumberFloatVal(xy.Y)})
	} else if fields["color_xy"] != nil && fields["mirek"] == nil && fields["kelvin"] == nil && remote.ColorTemperature != nil && remote.Color == nil {
		if oldXY == nil || oldMirek != nil || oldKelvin != nil {
			return false, fmt.Errorf("%s: color mode differs from state; resolve the local edit first", id)
		}
		xy, err := object(fields["color_xy"])
		if err != nil {
			return false, err
		}
		if len(xy) != 2 || xy["x"] == nil || xy["y"] == nil {
			return false, fmt.Errorf("color_xy must contain x and y")
		}
		for _, coord := range []struct {
			key string
			v   float64
		}{{"x", oldXY.X}, {"y", oldXY.Y}} {
			old := cty.NumberFloatVal(coord.v)
			if err := c.scalar(xy[coord.key], old, old, id, "color_xy."+coord.key); err != nil {
				return false, err
			}
		}
		if remote.ColorTemperature.Mirek <= 0 {
			return false, fmt.Errorf("invalid bridge mirek")
		}
		from, to = "color_xy", "mirek"
		value = cty.NumberIntVal(remote.ColorTemperature.Mirek)
	} else {
		return false, nil
	}
	r := fields[from].Range()
	tokens, d := hclsyntax.LexExpression(r.SliceBytes(c.Original), c.Path, r.Start)
	if d.HasErrors() {
		return false, fmt.Errorf("cannot parse color value")
	}
	for _, token := range tokens {
		if token.Type == hclsyntax.TokenComment {
			return false, fmt.Errorf("%s: color value contains comments; switch manually to preserve annotations", id)
		}
	}
	obj := action.(*hclsyntax.ObjectConsExpr)
	for _, item := range obj.Items {
		k, d := item.KeyExpr.Value(nil)
		if d.HasErrors() || k.AsString() != from {
			continue
		}
		r := item.KeyExpr.Range()
		c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), After: to, Light: id, Field: "color mode"})
		before := len(c.Edits)
		c.valueEdit(fields[from], value, to)
		c.Edits[before].Light = id
		return true, nil
	}
	return false, fmt.Errorf("color field not found")
}

// DecodeScene treats a null mirek as absent rather than numeric zero.
func DecodeScene(raw json.RawMessage) (hue.Scene, error) {
	var scene hue.Scene
	if err := json.Unmarshal(raw, &scene); err != nil {
		return scene, err
	}

	return scene, nil
}
