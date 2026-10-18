package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gradient, effects_v2 and dynamics of a scene action as typed attributes.
// They follow the API but flatten wrappers that hold one field, as palette
// does: a gradient point is the xy of its color, and effects_v2 is its action.
// dynamics keeps its one field, duration, which alone would not say what it
// times.

// Gradient modes from the Hue API v2 reference. The API defaults to the first.
var gradientModes = []string{"interpolated_palette", "interpolated_palette_mirrored", "random_pixelated", "segmented_palette"}

var gradientTypes = map[string]attr.Type{"points": types.ListType{ElemType: types.ObjectType{AttrTypes: xyTypes}}, "mode": types.StringType}
var dynamicsTypes = map[string]attr.Type{"duration": types.Int64Type}

func gradientAttribute() schema.SingleNestedAttribute {
	last := len(gradientModes) - 1
	return schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "Gradient for lights that support one. Preserved from the bridge when omitted.", Attributes: map[string]schema.Attribute{
		"points": schema.ListNestedAttribute{Required: true, Description: "Two to five colors as CIE xy, rounded to four decimal places. provider::hue::color_from_hex(...).color_xy returns one.", NestedObject: schema.NestedAttributeObject{Attributes: xyAttributes()}},
		"mode":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(gradientModes[0]), Description: "How the points are spread: " + strings.Join(gradientModes[:last], ", ") + " or " + gradientModes[last] + ". Defaults to " + gradientModes[0] + ", as in the API."},
	}}
}
func effectsV2Attribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "Effect, the action of the API's effects_v2. Preserved from the bridge when omitted. Use effect = \"no_effect\" to stop an effect.", Attributes: effectAttributes()}
}
func dynamicsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "Transition when the scene is recalled. Preserved from the bridge when omitted.", Attributes: map[string]schema.Attribute{
		"duration": schema.Int64Attribute{Required: true, Description: "Transition time in milliseconds. 0 changes the light at once."},
	}}
}

// The API's forms, as the provider sends and reads them.
type apiGradient struct {
	Points []apiGradientPoint `json:"points"`
	Mode   string             `json:"mode,omitempty"`
}
type apiGradientPoint struct {
	Color hue.ActionColor `json:"color"`
}
type apiDynamics struct {
	Duration *int64 `json:"duration,omitempty"`
}

type gradientModel struct {
	Points types.List   `tfsdk:"points"`
	Mode   types.String `tfsdk:"mode"`
}
type dynamicsModel struct {
	Duration types.Int64 `tfsdk:"duration"`
}

// gradientJSON returns the gradient that the provider sends for a known
// gradient. Written points are rounded as the bridge stores them, and points
// read from the bridge are sent as read.
func gradientJSON(ctx context.Context, gradient types.Object, written bool) (json.RawMessage, error) {
	m, err := objectAs[gradientModel](ctx, gradient)
	if err != nil {
		return nil, err
	}
	out := apiGradient{Points: []apiGradientPoint{}, Mode: gradientModes[0]}
	if known(m.Mode) {
		out.Mode = m.Mode.ValueString()
	}
	if known(m.Points) {
		for _, e := range m.Points.Elements() {
			p, _ := e.(types.Object)
			xy, ok := readXY(p)
			if !ok {
				return nil, fmt.Errorf("gradient point %d is not known", len(out.Points))
			}
			if written {
				xy = colors.Round(xy)
			}
			out.Points = append(out.Points, apiGradientPoint{Color: hue.ActionColor{XY: xy}})
		}
	}
	return json.Marshal(out)
}

// gradientFromJSON reads a gradient from the bridge, or from the JSON string
// that states before schema version 4 hold. Without a mode, it is the API's
// default, as mode defaults to in configuration.
func gradientFromJSON(raw []byte) (types.Object, error) {
	null := types.ObjectNull(gradientTypes)
	if len(raw) == 0 || string(raw) == "null" {
		return null, nil
	}
	var g apiGradient
	if err := json.Unmarshal(raw, &g); err != nil {
		return null, err
	}
	points := []attr.Value{}
	for _, p := range g.Points {
		points = append(points, xyValue(p.Color.XY))
	}
	if g.Mode == "" {
		g.Mode = gradientModes[0]
	}
	return types.ObjectValueMust(gradientTypes, map[string]attr.Value{"points": types.ListValueMust(types.ObjectType{AttrTypes: xyTypes}, points), "mode": types.StringValue(g.Mode)}), nil
}

// effectsV2JSON returns the effects_v2 that the provider sends for a known
// effect, rounded as gradientJSON rounds.
func effectsV2JSON(ctx context.Context, effect types.Object, written bool) (json.RawMessage, error) {
	e, err := objectAs[effectModel](ctx, effect)
	if err != nil {
		return nil, err
	}
	action, err := effectAction(ctx, e, written)
	if err != nil {
		return nil, err
	}
	return json.Marshal(apiEffect{Action: action})
}

// effectsV2FromJSON reads effects_v2 from the bridge, or from the JSON string
// that states before schema version 4 hold. Without an effect name, which the
// API requires, there is no effect.
func effectsV2FromJSON(raw []byte) (types.Object, error) {
	null := types.ObjectNull(effectTypes)
	if len(raw) == 0 || string(raw) == "null" {
		return null, nil
	}
	var e apiEffect
	if err := json.Unmarshal(raw, &e); err != nil {
		return null, err
	}
	if e.Action.Effect == "" {
		return null, nil
	}
	return effectValue(e.Action), nil
}

// dynamicsJSON returns the dynamics that the provider sends for known
// dynamics. A duration needs no rounding.
func dynamicsJSON(ctx context.Context, dynamics types.Object, _ bool) (json.RawMessage, error) {
	m, err := objectAs[dynamicsModel](ctx, dynamics)
	if err != nil {
		return nil, err
	}
	var out apiDynamics
	if known(m.Duration) {
		v := m.Duration.ValueInt64()
		out.Duration = &v
	}
	return json.Marshal(out)
}

// dynamicsFromJSON reads dynamics from the bridge, or from the JSON string
// that states before schema version 4 hold. Without a duration, there are no
// dynamics to manage.
func dynamicsFromJSON(raw []byte) (types.Object, error) {
	null := types.ObjectNull(dynamicsTypes)
	if len(raw) == 0 || string(raw) == "null" {
		return null, nil
	}
	var d apiDynamics
	if err := json.Unmarshal(raw, &d); err != nil {
		return null, err
	}
	if d.Duration == nil {
		return null, nil
	}
	return types.ObjectValueMust(dynamicsTypes, map[string]attr.Value{"duration": types.Int64Value(*d.Duration)}), nil
}

// reconcileObject keeps prior while the bridge stores what the provider sends
// for it, and reads the bridge's value otherwise. Both are compared in the
// form that the provider sends for written values, so that fields the
// provider does not model make no difference.
func reconcileObject(ctx context.Context, prior types.Object, raw []byte, send func(context.Context, types.Object, bool) (json.RawMessage, error), read func([]byte) (types.Object, error)) (types.Object, error) {
	stored, err := read(raw)
	if err != nil || !known(prior) || !known(stored) {
		return stored, err
	}
	a, err := send(ctx, prior, true)
	if err != nil {
		return stored, nil
	}
	b, err := send(ctx, stored, true)
	if err == nil && apiJSONEqual(a, b) {
		return prior, nil
	}
	return stored, nil
}

// validateActionObjects checks the limits in the Hue API v2 reference for the
// object attributes of the action at p. Each error points at the attribute
// that it is about.
func validateActionObjects(ctx context.Context, p path.Path, a actionModel) diag.Diagnostics {
	var diags diag.Diagnostics
	invalid := func(p path.Path, message string) {
		diags.AddAttributeError(p, "Invalid action", message)
	}
	if g, ok := knownObject[gradientModel](ctx, a.Gradient); ok {
		points := p.AtName("gradient").AtName("points")
		if known(g.Points) {
			if n := len(g.Points.Elements()); n < 2 || n > 5 {
				invalid(points, fmt.Sprintf("gradient.points must have 2 to 5 points, not %d.", n))
			}
			for i, e := range g.Points.Elements() {
				if o, ok := e.(types.Object); ok {
					if xy, ok := readXY(o); ok && !validXY(xy) {
						invalid(points.AtListIndex(i), "gradient points must have x >= 0, y >= 0 and x + y <= 1.")
					}
				}
			}
		}
		if known(g.Mode) && !slices.Contains(gradientModes, g.Mode.ValueString()) {
			invalid(p.AtName("gradient").AtName("mode"), fmt.Sprintf("gradient mode %q is not one of %s.", g.Mode.ValueString(), strings.Join(gradientModes, ", ")))
		}
	}
	if e, ok := knownObject[effectModel](ctx, a.EffectsV2); ok {
		validateEffect(ctx, p.AtName("effects_v2"), e, invalid)
		// Colors for an effect go in its parameters. no_effect is not checked,
		// since whether the bridge takes it with colors is not known.
		if known(e.Effect) && e.Effect.ValueString() != "no_effect" && (known(a.XY) || known(a.Mirek) || known(a.Gradient)) {
			invalid(p.AtName("effects_v2").AtName("effect"), "effects_v2.effect cannot be combined with color_xy, mirek or gradient. Configure effect colors in effects_v2.parameters instead.")
		}
	}
	if d, ok := knownObject[dynamicsModel](ctx, a.Dynamics); ok && known(d.Duration) && d.Duration.ValueInt64() < 0 {
		invalid(p.AtName("dynamics").AtName("duration"), "dynamics.duration must be 0 or more milliseconds.")
	}
	return diags
}

// knownObject decodes a known object. It reports false for an object that is
// null or unknown until apply.
func knownObject[T any](ctx context.Context, object types.Object) (T, bool) {
	var value T
	if !known(object) {
		return value, false
	}
	value, err := objectAs[T](ctx, object)
	return value, err == nil
}
