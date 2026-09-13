package provider

import (
	"encoding/json"
	"math"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Hue adds empty palette categories and rounds floating point values.
// Retain configured JSON formatting when these changes are semantically equal.
func palettesEqual(a, b []byte) bool {
	var x, y map[string]any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x == nil || y == nil {
		return false
	}
	for _, m := range []map[string]any{x, y} {
		for _, k := range []string{"color", "color_temperature", "dimming", "effects", "effects_v2"} {
			if v, ok := m[k].([]any); ok && len(v) == 0 {
				delete(m, k)
			}
		}
	}
	return paletteValueEqual(x, y)
}
func paletteValueEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && math.Abs(x-y) <= 1e-6
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !paletteValueEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !paletteValueEqual(v, y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
func reconcilePalette(prior types.String, raw []byte) (types.String, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull(), nil
	}
	if known(prior) && palettesEqual([]byte(prior.ValueString()), raw) {
		return prior, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return types.StringNull(), err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return types.StringNull(), err
	}
	return types.StringValue(string(canonical)), nil
}
