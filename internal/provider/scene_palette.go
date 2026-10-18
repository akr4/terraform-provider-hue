package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"reflect"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// palette is the API's scene palette as a typed attribute. It follows the API
// but flattens wrappers that hold one field, as the scene actions do:
// color.xy is color_xy, color_temperature.mirek is mirek, and
// dimming.brightness is brightness. An entry of effects_v2 is its action.

var paletteColorTypes = map[string]attr.Type{"color_xy": types.ObjectType{AttrTypes: xyTypes}, "brightness": types.Float64Type}
var paletteTemperatureTypes = map[string]attr.Type{"mirek": types.Int64Type, "brightness": types.Float64Type}
var paletteDimmingTypes = map[string]attr.Type{"brightness": types.Float64Type}
var paletteTypes = map[string]attr.Type{
	"color":             types.ListType{ElemType: types.ObjectType{AttrTypes: paletteColorTypes}},
	"color_temperature": types.ListType{ElemType: types.ObjectType{AttrTypes: paletteTemperatureTypes}},
	"dimming":           types.ListType{ElemType: types.ObjectType{AttrTypes: paletteDimmingTypes}},
	"effects_v2":        types.ListType{ElemType: types.ObjectType{AttrTypes: effectTypes}},
}

func paletteAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "Colors, color temperatures, brightness and effects that dynamic playback cycles through. Lists that you leave out are empty. Preserved on the bridge when omitted.", Attributes: map[string]schema.Attribute{
		"color": schema.ListNestedAttribute{Optional: true, Description: "Up to nine colors, each with a brightness. provider::hue::color_from_hex returns one.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"color_xy":   schema.SingleNestedAttribute{Required: true, Description: "CIE xy chromaticity, rounded to four decimal places.", Attributes: xyAttributes()},
			"brightness": schema.Float64Attribute{Required: true, Description: "Brightness from 0 to 100, rounded to two decimal places."},
		}}},
		"color_temperature": schema.ListNestedAttribute{Optional: true, Description: "At most one color temperature with a brightness.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"mirek":      schema.Int64Attribute{Required: true, Description: "Color temperature, 50–1000 mirek. Use provider::hue::mirek_from_kelvin for kelvin."},
			"brightness": schema.Float64Attribute{Required: true, Description: "Brightness from 0 to 100, rounded to two decimal places."},
		}}},
		"dimming": schema.ListNestedAttribute{Optional: true, Description: "At most one brightness.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"brightness": schema.Float64Attribute{Required: true, Description: "Brightness from 0 to 100, rounded to two decimal places."},
		}}},
		"effects_v2": schema.ListNestedAttribute{Optional: true, Description: "Up to three effects.", NestedObject: schema.NestedAttributeObject{Attributes: effectAttributes()}},
	}}
}

// The API's palette, as the provider sends and reads it.
type apiPalette struct {
	Color            []apiPaletteColor       `json:"color"`
	ColorTemperature []apiPaletteTemperature `json:"color_temperature"`
	Dimming          []hue.Dimming           `json:"dimming"`
	EffectsV2        []apiEffect             `json:"effects_v2"`
}
type apiPaletteColor struct {
	Color   hue.ActionColor `json:"color"`
	Dimming hue.Dimming     `json:"dimming"`
}
type apiPaletteTemperature struct {
	ColorTemperature hue.Temperature `json:"color_temperature"`
	Dimming          hue.Dimming     `json:"dimming"`
}

func emptyPalette() apiPalette {
	return apiPalette{Color: []apiPaletteColor{}, ColorTemperature: []apiPaletteTemperature{}, Dimming: []hue.Dimming{}, EffectsV2: []apiEffect{}}
}

// modeledPalette keeps the parts of a palette from the bridge that palette
// models, in the form that paletteJSON sends. It leaves out the deprecated
// effects list, which the bridge leaves empty but other apps may still write,
// and empty effect parameters. A palette that the bridge leaves out is empty.
func modeledPalette(raw []byte) ([]byte, error) {
	p := emptyPalette()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	for i, e := range p.EffectsV2 {
		if e.Action.Parameters != nil && *e.Action.Parameters == (apiEffectParameters{}) {
			p.EffectsV2[i].Action.Parameters = nil
		}
	}
	return json.Marshal(p)
}

// The Go form of palette. Lists stay types.List so that a list left out
// (null) differs from an empty one.
type paletteModel struct {
	Color            types.List `tfsdk:"color"`
	ColorTemperature types.List `tfsdk:"color_temperature"`
	Dimming          types.List `tfsdk:"dimming"`
	EffectsV2        types.List `tfsdk:"effects_v2"`
}
type paletteColorModel struct {
	XY         types.Object  `tfsdk:"color_xy"`
	Brightness types.Float64 `tfsdk:"brightness"`
}
type paletteTemperatureModel struct {
	Mirek      types.Int64   `tfsdk:"mirek"`
	Brightness types.Float64 `tfsdk:"brightness"`
}
type paletteDimmingModel struct {
	Brightness types.Float64 `tfsdk:"brightness"`
}

func listOf[T any](ctx context.Context, list types.List) ([]T, error) {
	var items []T
	if !known(list) {
		return items, nil
	}
	if d := list.ElementsAs(ctx, &items, false); d.HasError() {
		return nil, fmt.Errorf("%v", d)
	}
	return items, nil
}
func objectAs[T any](ctx context.Context, object types.Object) (T, error) {
	var value T
	if d := object.As(ctx, &value, basetypes.ObjectAsOptions{}); d.HasError() {
		return value, fmt.Errorf("%v", d)
	}
	return value, nil
}

// paletteJSON returns the palette that the provider sends for a known palette,
// rounded as the bridge stores it. Lists left out are empty.
func paletteJSON(ctx context.Context, palette types.Object) (json.RawMessage, error) {
	m, err := objectAs[paletteModel](ctx, palette)
	if err != nil {
		return nil, err
	}
	out := emptyPalette()
	paletteColors, err := listOf[paletteColorModel](ctx, m.Color)
	if err != nil {
		return nil, err
	}
	for _, c := range paletteColors {
		xy, _ := readXY(c.XY)
		out.Color = append(out.Color, apiPaletteColor{Color: hue.ActionColor{XY: colors.Round(xy)}, Dimming: hue.Dimming{Brightness: colors.RoundBrightness(c.Brightness.ValueFloat64())}})
	}
	temperatures, err := listOf[paletteTemperatureModel](ctx, m.ColorTemperature)
	if err != nil {
		return nil, err
	}
	for _, t := range temperatures {
		out.ColorTemperature = append(out.ColorTemperature, apiPaletteTemperature{ColorTemperature: hue.Temperature{Mirek: t.Mirek.ValueInt64()}, Dimming: hue.Dimming{Brightness: colors.RoundBrightness(t.Brightness.ValueFloat64())}})
	}
	dimming, err := listOf[paletteDimmingModel](ctx, m.Dimming)
	if err != nil {
		return nil, err
	}
	for _, d := range dimming {
		out.Dimming = append(out.Dimming, hue.Dimming{Brightness: colors.RoundBrightness(d.Brightness.ValueFloat64())})
	}
	effects, err := listOf[effectModel](ctx, m.EffectsV2)
	if err != nil {
		return nil, err
	}
	for _, e := range effects {
		action, err := effectAction(ctx, e)
		if err != nil {
			return nil, err
		}
		out.EffectsV2 = append(out.EffectsV2, apiEffect{Action: action})
	}
	return json.Marshal(out)
}

// paletteFromJSON reads a palette from the bridge, or from the JSON string that
// states before schema version 3 hold. Empty lists and empty effect parameters are left out, and a
// palette with no entries is null, so that generated configuration stays short.
func paletteFromJSON(raw []byte) (types.Object, error) {
	null := types.ObjectNull(paletteTypes)
	if len(raw) == 0 || string(raw) == "null" {
		return null, nil
	}
	var p apiPalette
	if err := json.Unmarshal(raw, &p); err != nil {
		return null, err
	}
	if len(p.Color)+len(p.ColorTemperature)+len(p.Dimming)+len(p.EffectsV2) == 0 {
		return null, nil
	}
	list := func(elem map[string]attr.Type, values []attr.Value) types.List {
		if len(values) == 0 {
			return types.ListNull(types.ObjectType{AttrTypes: elem})
		}
		return types.ListValueMust(types.ObjectType{AttrTypes: elem}, values)
	}
	var paletteColors, temperatures, dimming, effects []attr.Value
	for _, c := range p.Color {
		paletteColors = append(paletteColors, types.ObjectValueMust(paletteColorTypes, map[string]attr.Value{"color_xy": xyValue(c.Color.XY), "brightness": types.Float64Value(c.Dimming.Brightness)}))
	}
	for _, t := range p.ColorTemperature {
		temperatures = append(temperatures, types.ObjectValueMust(paletteTemperatureTypes, map[string]attr.Value{"mirek": types.Int64Value(t.ColorTemperature.Mirek), "brightness": types.Float64Value(t.Dimming.Brightness)}))
	}
	for _, d := range p.Dimming {
		dimming = append(dimming, types.ObjectValueMust(paletteDimmingTypes, map[string]attr.Value{"brightness": types.Float64Value(d.Brightness)}))
	}
	for _, e := range p.EffectsV2 {
		effects = append(effects, effectValue(e.Action))
	}
	return types.ObjectValueMust(paletteTypes, map[string]attr.Value{
		"color":             list(paletteColorTypes, paletteColors),
		"color_temperature": list(paletteTemperatureTypes, temperatures),
		"dimming":           list(paletteDimmingTypes, dimming),
		"effects_v2":        list(effectTypes, effects),
	}), nil
}

// paletteEqual reports whether the bridge stored what the provider sends for a
// known palette.
func paletteEqual(ctx context.Context, palette types.Object, raw []byte) bool {
	if !known(palette) {
		return false
	}
	sent, err := paletteJSON(ctx, palette)
	if err != nil {
		return false
	}
	stored, err := modeledPalette(raw)
	return err == nil && palettesEqual(sent, stored)
}

// reconcilePalette keeps the configured palette while the bridge stores it, and
// reads the bridge's palette otherwise.
func reconcilePalette(ctx context.Context, prior types.Object, raw []byte) (types.Object, error) {
	if paletteEqual(ctx, prior, raw) {
		return prior, nil
	}
	return paletteFromJSON(raw)
}

// validatePalette checks the limits in the Hue API v2 reference. Each
// error points at the attribute that it is about.
func validatePalette(ctx context.Context, palette types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if !known(palette) {
		return diags
	}
	root := path.Root("palette")
	invalid := func(p path.Path, message string) {
		diags.AddAttributeError(p, "Invalid palette", message)
	}
	m, err := objectAs[paletteModel](ctx, palette)
	if err != nil {
		invalid(root, err.Error())
		return diags
	}
	for _, limit := range []struct {
		name string
		list types.List
		max  int
	}{{"color", m.Color, 9}, {"color_temperature", m.ColorTemperature, 1}, {"dimming", m.Dimming, 1}, {"effects_v2", m.EffectsV2, 3}} {
		if known(limit.list) && len(limit.list.Elements()) > limit.max {
			invalid(root.AtName(limit.name), fmt.Sprintf("palette.%s can have at most %d entries.", limit.name, limit.max))
		}
	}
	brightness := func(p path.Path, v types.Float64) {
		if known(v) && !validBrightness(v.ValueFloat64()) {
			invalid(p, brightnessRange)
		}
	}
	mirek := func(p path.Path, v types.Int64) {
		if known(v) && !validMirek(v.ValueInt64()) {
			invalid(p, mirekRange)
		}
	}
	xy := func(p path.Path, v types.Object) {
		if c, ok := readXY(v); ok && !validXY(c) {
			invalid(p, xyRange)
		}
	}
	for i, c := range knownElements[paletteColorModel](ctx, m.Color) {
		p := root.AtName("color").AtListIndex(i)
		xy(p.AtName("color_xy"), c.XY)
		brightness(p.AtName("brightness"), c.Brightness)
	}
	for i, t := range knownElements[paletteTemperatureModel](ctx, m.ColorTemperature) {
		p := root.AtName("color_temperature").AtListIndex(i)
		mirek(p.AtName("mirek"), t.Mirek)
		brightness(p.AtName("brightness"), t.Brightness)
	}
	for i, d := range knownElements[paletteDimmingModel](ctx, m.Dimming) {
		brightness(root.AtName("dimming").AtListIndex(i).AtName("brightness"), d.Brightness)
	}
	for i, e := range knownElements[effectModel](ctx, m.EffectsV2) {
		validateEffect(ctx, root.AtName("effects_v2").AtListIndex(i), e, invalid)
	}
	return diags
}

// knownElements yields the index and value of each known element of a list.
// It skips elements that are unknown until apply, so that the others are still
// checked.
func knownElements[T any](ctx context.Context, list types.List) iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		if !known(list) {
			return
		}
		for i, e := range list.Elements() {
			o, ok := e.(types.Object)
			if !ok || !known(o) {
				continue
			}
			v, err := objectAs[T](ctx, o)
			if err != nil {
				continue
			}
			if !yield(i, v) {
				return
			}
		}
	}
}

// palettesEqual reports whether two palettes in the API's form are equal. Hue
// adds empty palette categories and rounds floating point values. It rounds
// scene brightness to two decimal places, so palette brightness is compared the
// same way.
func palettesEqual(a, b []byte) bool {
	var x, y map[string]any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x == nil || y == nil {
		return false
	}
	for _, m := range []map[string]any{x, y} {
		for _, k := range []string{"color", "color_temperature", "dimming", "effects", "effects_v2"} {
			// A category that is null has no entries either.
			if v, ok := m[k].([]any); m[k] == nil || (ok && len(v) == 0) {
				delete(m, k)
			}
		}
	}
	return apiValueEqual("", x, y)
}

// apiJSONEqual reports whether two values in the API's form are equal, with
// numbers compared as palettesEqual compares them.
func apiJSONEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return apiValueEqual("", x, y)
}
func apiValueEqual(key string, a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		if ok && key == "brightness" {
			return colors.EqualBrightness(x, y)
		}
		return ok && math.Abs(x-y) <= 1e-6
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !apiValueEqual(k, v, w) {
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
			if !apiValueEqual(key, v, y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
