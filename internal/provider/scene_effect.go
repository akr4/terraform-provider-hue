package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An effect is the action of the API's effects_v2, which scene actions and
// palette share. It flattens wrappers that hold one field:
// parameters.color.xy is parameters.color_xy, and
// parameters.color_temperature.mirek is parameters.mirek.

// Effect names from the Hue API v2 reference.
var effectNames = []string{"prism", "opal", "glisten", "sparkle", "fire", "candle", "underwater", "cosmos", "sunbeam", "enchant", "no_effect"}

var effectParameterTypes = map[string]attr.Type{"color_xy": types.ObjectType{AttrTypes: xyTypes}, "mirek": types.Int64Type, "speed": types.Float64Type}
var effectTypes = map[string]attr.Type{"effect": types.StringType, "parameters": types.ObjectType{AttrTypes: effectParameterTypes}}

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

// The API's effects_v2, as the provider sends and reads it.
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

type effectModel struct {
	Effect     types.String `tfsdk:"effect"`
	Parameters types.Object `tfsdk:"parameters"`
}
type effectParametersModel struct {
	XY    types.Object  `tfsdk:"color_xy"`
	Mirek types.Int64   `tfsdk:"mirek"`
	Speed types.Float64 `tfsdk:"speed"`
}

// effectAction returns the action that the provider sends for an effect.
// Written colors are rounded as the bridge stores them, and colors read from
// the bridge are sent as read.
func effectAction(ctx context.Context, e effectModel, written bool) (apiEffectAction, error) {
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
		if written {
			xy = colors.Round(xy)
		}
		parameters.Color = &hue.ActionColor{XY: xy}
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

// validateEffect checks the effect name and the parameter ranges in the Hue
// API v2 reference.
func validateEffect(ctx context.Context, p path.Path, e effectModel, invalid func(path.Path, string)) {
	if known(e.Effect) && !slices.Contains(effectNames, e.Effect.ValueString()) {
		invalid(p.AtName("effect"), fmt.Sprintf("effect %q is not one of %s.", e.Effect.ValueString(), strings.Join(effectNames, ", ")))
	}
	if !known(e.Parameters) {
		return
	}
	parameters, err := objectAs[effectParametersModel](ctx, e.Parameters)
	if err != nil {
		return
	}
	p = p.AtName("parameters")
	if xy, ok := readXY(parameters.XY); ok && !validXY(xy) {
		invalid(p.AtName("color_xy"), xyRange)
	}
	if known(parameters.Mirek) && !validMirek(parameters.Mirek.ValueInt64()) {
		invalid(p.AtName("mirek"), mirekRange)
	}
	if known(parameters.Speed) && (parameters.Speed.ValueFloat64() < 0 || parameters.Speed.ValueFloat64() > 1) {
		invalid(p.AtName("speed"), "effect speed must be between 0 and 1.")
	}
}
