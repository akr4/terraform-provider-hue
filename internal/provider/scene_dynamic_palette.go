package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// dynamic_palette is the typed form of the API's scene palette. It follows the
// API but flattens wrappers that hold one field, as the scene actions do:
// color.xy is color_xy, color_temperature.mirek is mirek, and
// dimming.brightness is brightness.

// Effect names from the Hue API v2 reference.
var effectNames = []string{"prism", "opal", "glisten", "sparkle", "fire", "candle", "underwater", "cosmos", "sunbeam", "enchant", "no_effect"}

var effectParameterTypes = map[string]attr.Type{"color_xy": types.ObjectType{AttrTypes: xyTypes}, "mirek": types.Int64Type, "speed": types.Float64Type}
var effectTypes = map[string]attr.Type{"effect": types.StringType, "parameters": types.ObjectType{AttrTypes: effectParameterTypes}}
var paletteColorTypes = map[string]attr.Type{"color_xy": types.ObjectType{AttrTypes: xyTypes}, "brightness": types.Float64Type}
var paletteTemperatureTypes = map[string]attr.Type{"mirek": types.Int64Type, "brightness": types.Float64Type}
var paletteDimmingTypes = map[string]attr.Type{"brightness": types.Float64Type}
var dynamicPaletteTypes = map[string]attr.Type{
	"color":             types.ListType{ElemType: types.ObjectType{AttrTypes: paletteColorTypes}},
	"color_temperature": types.ListType{ElemType: types.ObjectType{AttrTypes: paletteTemperatureTypes}},
	"dimming":           types.ListType{ElemType: types.ObjectType{AttrTypes: paletteDimmingTypes}},
	"effects":           types.ListType{ElemType: types.ObjectType{AttrTypes: effectTypes}},
}

func effectAttributes() map[string]schema.Attribute {
	last := len(effectNames) - 1
	return map[string]schema.Attribute{
		"effect": schema.StringAttribute{Required: true, Description: "Effect name: " + strings.Join(effectNames[:last], ", ") + " or " + effectNames[last] + "."},
		"parameters": schema.SingleNestedAttribute{Optional: true, Description: "Effect parameters.", Attributes: map[string]schema.Attribute{
			"color_xy": schema.SingleNestedAttribute{Optional: true, Description: "Effect color as CIE xy, rounded to four decimal places.", Attributes: xyAttributes()},
			"mirek":    schema.Int64Attribute{Optional: true, Description: "Effect color temperature, 50–1000 mirek."},
			"speed":    schema.Float64Attribute{Optional: true, Description: "Effect speed from 0 to 1."},
		}},
	}
}
func dynamicPaletteAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "Colors, color temperatures, brightness and effects that dynamic playback cycles through. Lists that you leave out are empty. Preserved on the bridge when omitted. Conflicts with palette.", Attributes: map[string]schema.Attribute{
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
		"effects": schema.ListNestedAttribute{Optional: true, Description: "Up to three effects.", NestedObject: schema.NestedAttributeObject{Attributes: effectAttributes()}},
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
type apiEffect struct {
	Action apiEffectAction `json:"action"`
}
type apiEffectAction struct {
	Effect     string               `json:"effect"`
	Parameters *apiEffectParameters `json:"parameters,omitempty"`
}
type apiEffectParameters struct {
	Color            *hue.ActionColor `json:"color,omitempty"`
	ColorTemperature *hue.Temperature `json:"color_temperature,omitempty"`
	Speed            *float64         `json:"speed,omitempty"`
}

func emptyPalette() apiPalette {
	return apiPalette{Color: []apiPaletteColor{}, ColorTemperature: []apiPaletteTemperature{}, Dimming: []hue.Dimming{}, EffectsV2: []apiEffect{}}
}

// modeledPalette keeps the parts of a palette from the bridge that
// dynamic_palette models, in the form that dynamicPaletteJSON sends. It leaves
// out the deprecated effects list, which the bridge leaves empty but other
// apps may still write, and empty effect parameters. A palette that the bridge
// leaves out is empty.
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

// The Go form of dynamic_palette. Lists stay types.List so that a list left out
// (null) differs from an empty one.
type dynamicPaletteModel struct {
	Color            types.List `tfsdk:"color"`
	ColorTemperature types.List `tfsdk:"color_temperature"`
	Dimming          types.List `tfsdk:"dimming"`
	Effects          types.List `tfsdk:"effects"`
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
type effectModel struct {
	Effect     types.String `tfsdk:"effect"`
	Parameters types.Object `tfsdk:"parameters"`
}
type effectParametersModel struct {
	XY    types.Object  `tfsdk:"color_xy"`
	Mirek types.Int64   `tfsdk:"mirek"`
	Speed types.Float64 `tfsdk:"speed"`
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

// dynamicPaletteJSON returns the palette that the provider sends for a known
// dynamic_palette, rounded as the bridge stores it. Lists left out are empty.
func dynamicPaletteJSON(ctx context.Context, palette types.Object) (json.RawMessage, error) {
	m, err := objectAs[dynamicPaletteModel](ctx, palette)
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
	effects, err := listOf[effectModel](ctx, m.Effects)
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
func effectAction(ctx context.Context, e effectModel) (apiEffectAction, error) {
	action := apiEffectAction{Effect: e.Effect.ValueString()}
	if !known(e.Parameters) {
		return action, nil
	}
	p, err := objectAs[effectParametersModel](ctx, e.Parameters)
	if err != nil {
		return action, err
	}
	var parameters apiEffectParameters
	if xy, ok := readXY(p.XY); ok {
		parameters.Color = &hue.ActionColor{XY: colors.Round(xy)}
	}
	if known(p.Mirek) {
		parameters.ColorTemperature = &hue.Temperature{Mirek: p.Mirek.ValueInt64()}
	}
	if known(p.Speed) {
		v := p.Speed.ValueFloat64()
		parameters.Speed = &v
	}
	// Empty parameters are left out, so that the bridge has nothing to drop.
	if parameters != (apiEffectParameters{}) {
		action.Parameters = &parameters
	}
	return action, nil
}

// dynamicPaletteFromJSON reads a palette from the bridge, or from a palette
// JSON attribute. Empty lists and empty effect parameters are left out, and a
// palette with no entries is null, so that generated configuration stays short.
func dynamicPaletteFromJSON(raw []byte) (types.Object, error) {
	null := types.ObjectNull(dynamicPaletteTypes)
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
	return types.ObjectValueMust(dynamicPaletteTypes, map[string]attr.Value{
		"color":             list(paletteColorTypes, paletteColors),
		"color_temperature": list(paletteTemperatureTypes, temperatures),
		"dimming":           list(paletteDimmingTypes, dimming),
		"effects":           list(effectTypes, effects),
	}), nil
}
func effectValue(a apiEffectAction) types.Object {
	parameters := types.ObjectNull(effectParameterTypes)
	if p := a.Parameters; p != nil && *p != (apiEffectParameters{}) {
		xy, mirek, speed := types.ObjectNull(xyTypes), types.Int64Null(), types.Float64Null()
		if p.Color != nil {
			xy = xyValue(p.Color.XY)
		}
		if p.ColorTemperature != nil {
			mirek = types.Int64Value(p.ColorTemperature.Mirek)
		}
		if p.Speed != nil {
			speed = types.Float64Value(*p.Speed)
		}
		parameters = types.ObjectValueMust(effectParameterTypes, map[string]attr.Value{"color_xy": xy, "mirek": mirek, "speed": speed})
	}
	return types.ObjectValueMust(effectTypes, map[string]attr.Value{"effect": types.StringValue(a.Effect), "parameters": parameters})
}

// dynamicPaletteEqual reports whether the bridge stored what the provider sends
// for a known dynamic_palette.
func dynamicPaletteEqual(ctx context.Context, palette types.Object, raw []byte) bool {
	if !known(palette) {
		return false
	}
	sent, err := dynamicPaletteJSON(ctx, palette)
	if err != nil {
		return false
	}
	stored, err := modeledPalette(raw)
	return err == nil && palettesEqual(sent, stored)
}

// reconcileDynamicPalette keeps the configured dynamic_palette while the bridge
// stores it, and reads the bridge's palette otherwise.
func reconcileDynamicPalette(ctx context.Context, prior types.Object, raw []byte) (types.Object, error) {
	if dynamicPaletteEqual(ctx, prior, raw) {
		return prior, nil
	}
	return dynamicPaletteFromJSON(raw)
}

// validateDynamicPalette checks the limits in the Hue API v2 reference. Each
// error points at the attribute that it is about.
func validateDynamicPalette(ctx context.Context, palette types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if !known(palette) {
		return diags
	}
	root := path.Root("dynamic_palette")
	invalid := func(p path.Path, message string) {
		diags.AddAttributeError(p, "Invalid dynamic_palette", message)
	}
	m, err := objectAs[dynamicPaletteModel](ctx, palette)
	if err != nil {
		invalid(root, err.Error())
		return diags
	}
	for _, limit := range []struct {
		name string
		list types.List
		max  int
	}{{"color", m.Color, 9}, {"color_temperature", m.ColorTemperature, 1}, {"dimming", m.Dimming, 1}, {"effects", m.Effects, 3}} {
		if known(limit.list) && len(limit.list.Elements()) > limit.max {
			invalid(root.AtName(limit.name), fmt.Sprintf("dynamic_palette.%s can have at most %d entries.", limit.name, limit.max))
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
	for i, e := range knownElements[effectModel](ctx, m.Effects) {
		p := root.AtName("effects").AtListIndex(i)
		if known(e.Effect) && !slices.Contains(effectNames, e.Effect.ValueString()) {
			invalid(p.AtName("effect"), fmt.Sprintf("effect %q is not one of %s.", e.Effect.ValueString(), strings.Join(effectNames, ", ")))
		}
		if !known(e.Parameters) {
			continue
		}
		parameters, err := objectAs[effectParametersModel](ctx, e.Parameters)
		if err != nil {
			continue
		}
		p = p.AtName("parameters")
		xy(p.AtName("color_xy"), parameters.XY)
		mirek(p.AtName("mirek"), parameters.Mirek)
		if known(parameters.Speed) && (parameters.Speed.ValueFloat64() < 0 || parameters.Speed.ValueFloat64() > 1) {
			invalid(p.AtName("speed"), "effect speed must be between 0 and 1.")
		}
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

// planPalettes plans palette and dynamic_palette. The state holds the palette
// in the form that the configuration uses: palette while it is configured, and
// dynamic_palette otherwise. With neither configured, the bridge keeps its
// palette, and a state that holds it as palette, as every state from an
// earlier version does, moves it to dynamic_palette. prior is nil for a new
// scene.
func planPalettes(config sceneModel, planned *sceneModel, prior *sceneModel) {
	switch {
	case !config.Palette.IsNull():
		planned.DynamicPalette = types.ObjectNull(dynamicPaletteTypes)
	case !config.DynamicPalette.IsNull() || prior == nil:
		planned.Palette = types.StringNull()
	case !prior.Palette.IsNull():
		planned.Palette = types.StringNull()
		// A palette that dynamic_palette cannot read is read from the bridge
		// after apply.
		planned.DynamicPalette = types.ObjectUnknown(dynamicPaletteTypes)
		if dynamic, err := dynamicPaletteFromJSON([]byte(prior.Palette.ValueString())); err == nil {
			planned.DynamicPalette = dynamic
		}
	default:
		planned.Palette = types.StringNull()
		planned.DynamicPalette = prior.DynamicPalette
	}
}
