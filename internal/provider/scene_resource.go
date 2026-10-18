package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type sceneResource struct{ client *hue.Client }
type sceneModel struct {
	ID          types.String  `tfsdk:"id"`
	Name        types.String  `tfsdk:"name"`
	Group       types.String  `tfsdk:"group"`
	Actions     types.Map     `tfsdk:"actions"`
	Speed       types.Float64 `tfsdk:"speed"`
	AutoDynamic types.Bool    `tfsdk:"auto_dynamic"`
	Palette     types.Object  `tfsdk:"palette"`
}
type actionModel struct {
	Gradient   types.Object  `tfsdk:"gradient"`
	Effects    types.String  `tfsdk:"effects"`
	EffectsV2  types.Object  `tfsdk:"effects_v2"`
	Dynamics   types.Object  `tfsdk:"dynamics"`
	On         types.Bool    `tfsdk:"on"`
	Brightness types.Float64 `tfsdk:"brightness"`
	Mirek      types.Int64   `tfsdk:"mirek"`
	Kelvin     types.Int64   `tfsdk:"kelvin"`
	XY         types.Object  `tfsdk:"color_xy"`
}

var xyTypes = map[string]attr.Type{"x": types.Float64Type, "y": types.Float64Type}
var actionTypes = map[string]attr.Type{"gradient": types.ObjectType{AttrTypes: gradientTypes}, "effects": types.StringType, "effects_v2": types.ObjectType{AttrTypes: effectTypes}, "dynamics": types.ObjectType{AttrTypes: dynamicsTypes}, "on": types.BoolType, "brightness": types.Float64Type, "mirek": types.Int64Type, "kelvin": types.Int64Type, "color_xy": types.ObjectType{AttrTypes: xyTypes}}
var actionType = types.ObjectType{AttrTypes: actionTypes}

func known(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
func xyValue(p hue.XY) types.Object {
	return types.ObjectValueMust(xyTypes, map[string]attr.Value{"x": types.Float64Value(p.X), "y": types.Float64Value(p.Y)})
}
func readXY(v types.Object) (hue.XY, bool) {
	if !known(v) {
		return hue.XY{}, false
	}
	x, xok := v.Attributes()["x"].(types.Float64)
	y, yok := v.Attributes()["y"].(types.Float64)
	return hue.XY{X: x.ValueFloat64(), Y: y.ValueFloat64()}, xok && yok && known(x) && known(y)
}

// The API's value ranges, which scene actions and palette share, and the
// messages for values outside them.
func validXY(p hue.XY) bool {
	return p.X >= 0 && p.Y >= 0 && p.X <= 1 && p.Y <= 1 && p.X+p.Y <= 1+1e-12 && !math.IsNaN(p.X) && !math.IsNaN(p.Y)
}
func validBrightness(v float64) bool { return v >= 0 && v <= 100 }
func validMirek(v int64) bool        { return v >= colors.MinMirek && v <= colors.MaxMirek }

var (
	xyRange         = "color_xy must have x >= 0, y >= 0 and x + y <= 1."
	brightnessRange = "brightness must be between 0 and 100."
	mirekRange      = fmt.Sprintf("mirek must be between %d and %d.", colors.MinMirek, colors.MaxMirek)
)

func xyAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{"x": schema.Float64Attribute{Required: true}, "y": schema.Float64Attribute{Required: true}}
}
func emptyAction() actionModel {
	return actionModel{Gradient: types.ObjectNull(gradientTypes), Effects: types.StringNull(), EffectsV2: types.ObjectNull(effectTypes), Dynamics: types.ObjectNull(dynamicsTypes), On: types.BoolNull(), Brightness: types.Float64Null(), Mirek: types.Int64Null(), Kelvin: types.Int64Null(), XY: types.ObjectNull(xyTypes)}
}
func actionsFrom(ctx context.Context, m types.Map) (map[string]actionModel, diag.Diagnostics) {
	result := map[string]actionModel{}
	if m.IsNull() || m.IsUnknown() {
		return result, nil
	}
	d := m.ElementsAs(ctx, &result, false)
	return result, d
}
func (r *sceneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scene"
}
func (r *sceneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Version: 4, Description: "Manage a Hue scene. Changing its group replaces it.", Attributes: map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Bridge resource UUID."},
		"name":         schema.StringAttribute{Required: true, Description: "Scene name."},
		"group":        schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Room or zone UUID. Changes replace the scene."},
		"speed":        schema.Float64Attribute{Optional: true, Computed: true, Description: "Dynamic scene speed from 0 to 1."},
		"auto_dynamic": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Enable dynamic playback. Defaults to false."},
		"palette":      paletteAttribute(),
		"actions": schema.MapNestedAttribute{Required: true, Description: "Actions keyed by light UUID.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"gradient":   gradientAttribute(),
			"effects":    schema.StringAttribute{Optional: true, Computed: true, Description: "The API's deprecated effects as a JSON object; use jsonencode. Use effects_v2 instead. Preserved from the bridge when omitted."},
			"effects_v2": effectsV2Attribute(),
			"dynamics":   dynamicsAttribute(),
			"on":         schema.BoolAttribute{Optional: true, Description: "On/off state."},
			"brightness": schema.Float64Attribute{Optional: true, Description: "Brightness from 0 to 100, rounded to two decimal places."},
			"mirek":      schema.Int64Attribute{Optional: true, Description: "Color temperature, 50–1000 mirek. Individual lights may support a narrower range. Use provider::hue::mirek_from_kelvin for kelvin."},
			"kelvin":     schema.Int64Attribute{Optional: true, Description: "Removed in 0.2.0, and an error when set. Use mirek instead; provider::hue::mirek_from_kelvin converts kelvin.", DeprecationMessage: kelvinRemoved + " Use mirek instead."},
			"color_xy":   schema.SingleNestedAttribute{Optional: true, Description: "CIE xy chromaticity, rounded to four decimal places. Brightness is configured separately.", Attributes: xyAttributes()},
		}}},
	}}
}
func (r *sceneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*hue.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func (r *sceneResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if known(m.Name) && m.Name.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "name must not be empty.")
	}
	if known(m.Group) && !validUUID(m.Group.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("group"), "Invalid UUID", "group must be a UUID.")
	}
	if known(m.Speed) && (m.Speed.ValueFloat64() < 0 || m.Speed.ValueFloat64() > 1) {
		resp.Diagnostics.AddAttributeError(path.Root("speed"), "Invalid speed", "speed must be between 0 and 1.")
	}
	resp.Diagnostics.Append(validatePalette(ctx, m.Palette)...)
	// Individual map values may be unknown while referenced resources are planned.
	if !known(m.Actions) {
		return
	}
	for id, value := range m.Actions.Elements() {
		p := path.Root("actions").AtMapKey(id)
		if !validUUID(id) {
			resp.Diagnostics.AddAttributeError(p, "Invalid light ID", "Action keys must be light UUIDs.")
		}
		if value.IsNull() {
			resp.Diagnostics.AddAttributeError(p, "Invalid action", "Actions must not be null.")
			continue
		}
		if value.IsUnknown() {
			continue
		}
		var a actionModel
		// Errors in other actions do not stop this one from being checked.
		d := value.(types.Object).As(ctx, &a, basetypes.ObjectAsOptions{})
		resp.Diagnostics.Append(d...)
		if d.HasError() {
			continue
		}
		// Terraform drops an attribute that a nested object does not have
		// without an error, so the schema keeps kelvin to report its removal.
		if !a.Kelvin.IsNull() {
			resp.Diagnostics.AddAttributeError(p.AtName("kelvin"), "kelvin was removed", kelvinReplacement(a.Kelvin))
		}
		for _, message := range validateAction(a) {
			resp.Diagnostics.AddAttributeError(p, "Invalid action", message)
		}
		resp.Diagnostics.Append(validateActionObjects(ctx, p, a)...)
	}
}

const kelvinRemoved = "kelvin was removed in 0.2.0."

// kelvinReplacement tells what to write instead of a configured kelvin: the
// mirek that earlier versions sent for it, and provider::hue::mirek_from_kelvin
// where it returns the same mirek. The function needs Terraform 1.8.
func kelvinReplacement(kelvin types.Int64) string {
	if !known(kelvin) || kelvin.ValueInt64() <= 0 {
		return kelvinRemoved + " Write mirek instead. provider::hue::mirek_from_kelvin converts kelvin."
	}
	k := kelvin.ValueInt64()
	m := colors.MirekForKelvin(k)
	if colors.KelvinToMirek(k) != m {
		return fmt.Sprintf("%s Write mirek = %d instead, the mirek that earlier versions sent for kelvin = %d.", kelvinRemoved, m, k)
	}
	return fmt.Sprintf("%s Write mirek = provider::hue::mirek_from_kelvin(%d) instead, or mirek = %d before Terraform 1.8.", kelvinRemoved, k, m)
}
func validateAction(a actionModel) []string {
	var errs []string
	if known(a.Effects) && hue.ValidateConfiguration([]byte(a.Effects.ValueString())) != nil {
		errs = append(errs, "effects must be a JSON object.")
	}
	if known(a.EffectsV2) {
		effect, _ := a.EffectsV2.Attributes()["effect"].(types.String)
		if known(effect) && effect.ValueString() != "no_effect" && (known(a.XY) || known(a.Mirek) || known(a.Gradient)) {
			errs = append(errs, "effects_v2.effect cannot be combined with color_xy, mirek or gradient. Configure effect colors in effects_v2.parameters instead.")
		}
	}
	if !a.XY.IsNull() && !a.Mirek.IsNull() {
		errs = append(errs, "color_xy cannot be combined with mirek. A light shows either a color or a color temperature.")
	}
	if known(a.Mirek) && !validMirek(a.Mirek.ValueInt64()) {
		errs = append(errs, mirekRange)
	}
	if known(a.Brightness) && !validBrightness(a.Brightness.ValueFloat64()) {
		errs = append(errs, brightnessRange)
	}
	if p, ok := readXY(a.XY); ok && !validXY(p) {
		errs = append(errs, xyRange)
	}
	return errs
}
func (r *sceneResource) body(ctx context.Context, m sceneModel, config sceneModel, create bool) (hue.Scene, error) {
	scene := hue.Scene{Metadata: hue.Metadata{Name: m.Name.ValueString()}, Actions: []hue.SceneAction{}}
	if create {
		group, err := hue.GetOne[hue.Group](ctx, r.client, "room", m.Group.ValueString())
		kind := "room"
		if hue.IsNotFound(err) {
			group, err = hue.GetOne[hue.Group](ctx, r.client, "zone", m.Group.ValueString())
			kind = "zone"
		}
		if err != nil {
			return scene, fmt.Errorf("resolve scene group: %w", err)
		}
		scene.Group = hue.Reference{RID: group.ID, RType: kind}
	}
	if known(m.Speed) {
		v := m.Speed.ValueFloat64()
		scene.Speed = &v
	}
	if known(m.AutoDynamic) {
		v := m.AutoDynamic.ValueBool()
		scene.AutoDynamic = &v
	}
	if known(config.Palette) {
		palette, err := paletteJSON(ctx, m.Palette)
		if err != nil {
			return scene, fmt.Errorf("invalid palette: %w", err)
		}
		scene.Palette = palette
	}
	actions, d := actionsFrom(ctx, m.Actions)
	if d.HasError() {
		return scene, fmt.Errorf("decode planned actions: %v", d)
	}
	configured, d := actionsFrom(ctx, config.Actions)
	if d.HasError() {
		return scene, fmt.Errorf("decode configured actions: %v", d)
	}
	ids := []string{}
	for id := range actions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := actions[id]
		c, exists := configured[id]
		if !exists {
			return scene, fmt.Errorf("missing configured action %s", id)
		}
		action := hue.Action{}
		if known(a.Effects) {
			raw := json.RawMessage(a.Effects.ValueString())
			if err := hue.ValidateConfiguration(raw); err != nil {
				return scene, fmt.Errorf("invalid effects action: %w", err)
			}
			action.Effects = raw
		}
		for _, o := range []struct {
			name  string
			value types.Object
			send  func(context.Context, types.Object) (json.RawMessage, error)
			raw   *json.RawMessage
		}{{"gradient", a.Gradient, gradientJSON, &action.Gradient}, {"effects_v2", a.EffectsV2, effectsV2JSON, &action.EffectsV2}, {"dynamics", a.Dynamics, dynamicsJSON, &action.Dynamics}} {
			if !known(o.value) {
				continue
			}
			raw, err := o.send(ctx, o.value)
			if err != nil {
				return scene, fmt.Errorf("invalid %s action: %w", o.name, err)
			}
			*o.raw = raw
		}
		if known(a.On) {
			action.On = &hue.On{On: a.On.ValueBool()}
		}
		if known(a.Brightness) {
			action.Dimming = &hue.Dimming{Brightness: colors.RoundBrightness(a.Brightness.ValueFloat64())}
		}
		// Only configured colors and color temperatures are sent.
		if known(c.Mirek) {
			action.ColorTemperature = &hue.Temperature{Mirek: c.Mirek.ValueInt64()}
		}
		if p, ok := readXY(c.XY); ok {
			action.Color = &hue.ActionColor{XY: colors.Round(p)}
		}
		scene.Actions = append(scene.Actions, hue.SceneAction{Target: hue.Reference{RID: id, RType: "light"}, Action: action})
	}
	return scene, nil
}

// reconcileAction keeps the user's exact representation when the bridge stored
// what the provider sent for it. The bridge stores xy and mirek as sent, even
// outside the light's gamut or range, and rounds brightness to two decimal places.
func reconcileAction(ctx context.Context, prior actionModel, actual hue.Action) (actionModel, error) {
	next := emptyAction()
	next.Effects = sceneJSONValue(prior.Effects, actual.Effects)
	var err error
	if next.Gradient, err = reconcileObject(ctx, prior.Gradient, actual.Gradient, gradientJSON, gradientFromJSON); err != nil {
		return next, fmt.Errorf("read gradient: %w", err)
	}
	if next.EffectsV2, err = reconcileObject(ctx, prior.EffectsV2, actual.EffectsV2, effectsV2JSON, effectsV2FromJSON); err != nil {
		return next, fmt.Errorf("read effects_v2: %w", err)
	}
	if next.Dynamics, err = reconcileObject(ctx, prior.Dynamics, actual.Dynamics, dynamicsJSON, dynamicsFromJSON); err != nil {
		return next, fmt.Errorf("read dynamics: %w", err)
	}
	if actual.On != nil {
		next.On = types.BoolValue(actual.On.On)
	}
	if actual.Dimming != nil {
		next.Brightness = types.Float64Value(actual.Dimming.Brightness)
		if known(prior.Brightness) && colors.EqualBrightness(prior.Brightness.ValueFloat64(), actual.Dimming.Brightness) {
			next.Brightness = prior.Brightness
		}
	}
	if actual.Color != nil {
		next.XY = xyValue(actual.Color.XY)
		if expected, ok := readXY(prior.XY); ok && colors.EqualXY(expected, actual.Color.XY) {
			next.XY = prior.XY
		}
	}
	if actual.ColorTemperature != nil {
		m := actual.ColorTemperature.Mirek
		next.Mirek = types.Int64Value(m)
		if known(prior.Mirek) && colors.EqualMirek(prior.Mirek.ValueInt64(), m) {
			next.Mirek = prior.Mirek
		}
	}
	return next, nil
}
func (r *sceneResource) refresh(ctx context.Context, m *sceneModel) error {
	scene, err := hue.GetOne[hue.Scene](ctx, r.client, "scene", m.ID.ValueString())
	if err != nil {
		return err
	}
	prior, d := actionsFrom(ctx, m.Actions)
	if d.HasError() {
		return fmt.Errorf("decode previous actions: %v", d)
	}
	actions := map[string]actionModel{}
	for _, item := range scene.Actions {
		if item.Target.RType != "light" {
			return fmt.Errorf("unsupported scene action target %q", item.Target.RType)
		}
		old, ok := prior[item.Target.RID]
		if !ok {
			old = emptyAction()
		}
		if _, duplicate := actions[item.Target.RID]; duplicate {
			return fmt.Errorf("duplicate scene action for light %s", item.Target.RID)
		}
		if actions[item.Target.RID], err = reconcileAction(ctx, old, item.Action); err != nil {
			return fmt.Errorf("scene action for light %s: %w", item.Target.RID, err)
		}
	}
	m.Name = types.StringValue(scene.Metadata.Name)
	m.Group = types.StringValue(scene.Group.RID)
	m.Speed = types.Float64Null()
	if scene.Speed != nil {
		m.Speed = types.Float64Value(*scene.Speed)
	}
	m.AutoDynamic = types.BoolValue(false)
	if scene.AutoDynamic != nil {
		m.AutoDynamic = types.BoolValue(*scene.AutoDynamic)
	}
	if m.Palette, err = reconcilePalette(ctx, m.Palette, scene.Palette); err != nil {
		return err
	}
	m.Actions, d = types.MapValueFrom(ctx, actionType, actions)
	if d.HasError() {
		return fmt.Errorf("encode actions: %v", d)
	}
	return nil
}
func (r *sceneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m, config sceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m, config, true)
	if err != nil {
		resp.Diagnostics.AddError("Invalid scene", err.Error())
		return
	}
	id, err := r.client.Create(ctx, "scene", body)
	if err != nil {
		resp.Diagnostics.AddError("Create scene failed", err.Error())
		return
	}
	m.ID = types.StringValue(id)
	// Record the identity even if a subsequent refresh fails, so Terraform can
	// recover without orphaning the newly created scene.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), m.ID)...)
	if err = r.refresh(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Read created scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.refresh(ctx, &m)
	if hue.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, config sceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m, config, false)
	if err == nil {
		// Images are unmanaged: omit metadata.image, including null.
		// group is immutable and must not be included in PUT.
		payload := map[string]any{"metadata": body.Metadata, "actions": body.Actions}
		if len(body.Palette) > 0 {
			payload["palette"] = body.Palette
		}
		if body.Speed != nil {
			payload["speed"] = body.Speed
		}
		if body.AutoDynamic != nil {
			payload["auto_dynamic"] = body.AutoDynamic
		}
		err = r.client.Update(ctx, "scene", m.ID.ValueString(), payload)
	}
	if err != nil {
		resp.Diagnostics.AddError("Update scene failed", err.Error())
		return
	}
	if err = r.refresh(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Read updated scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, "scene", m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete scene failed", err.Error())
	}
}
func (r *sceneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
}

// ModifyPlan plans the values that the bridge keeps when they are omitted,
// without bridge access.
func (r *sceneResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var config, planned, prior sceneModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() && known(config.Group) && config.Group.Equal(prior.Group) {
		if config.Speed.IsNull() {
			planned.Speed = prior.Speed
		}
		if config.Palette.IsNull() {
			planned.Palette = prior.Palette
		}
	}
	if known(config.Actions) && known(planned.Actions) {
		oldActions := map[string]actionModel{}
		if !req.State.Raw.IsNull() {
			var d diag.Diagnostics
			oldActions, d = actionsFrom(ctx, prior.Actions)
			resp.Diagnostics.Append(d...)
		}
		configured := config.Actions.Elements()
		elements := map[string]attr.Value{}
		for id, value := range planned.Actions.Elements() {
			elements[id] = value
			// An action that is unknown until apply keeps Terraform's plan; the
			// other actions, speed and palette are still planned.
			c, cok := configured[id].(types.Object)
			p, pok := value.(types.Object)
			if !cok || !pok || !known(c) || !known(p) {
				continue
			}
			var conf, plan actionModel
			resp.Diagnostics.Append(c.As(ctx, &conf, basetypes.ObjectAsOptions{})...)
			resp.Diagnostics.Append(p.As(ctx, &plan, basetypes.ObjectAsOptions{})...)
			if resp.Diagnostics.HasError() {
				return
			}
			old, ok := oldActions[id]
			if !ok {
				old = emptyAction()
			}
			action, d := types.ObjectValueFrom(ctx, actionTypes, planAction(conf, plan, old))
			resp.Diagnostics.Append(d...)
			elements[id] = action
		}
		actions, d := types.MapValue(actionType, elements)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		planned.Actions = actions
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &planned)...)
}
func planAction(config, planned, prior actionModel) actionModel {
	if config.Gradient.IsNull() {
		planned.Gradient = prior.Gradient
	}
	if config.Effects.IsNull() {
		planned.Effects = prior.Effects
	}
	if config.EffectsV2.IsNull() {
		planned.EffectsV2 = prior.EffectsV2
	}
	if config.Dynamics.IsNull() {
		planned.Dynamics = prior.Dynamics
	}
	return planned
}

// Keep equivalent user JSON (including jsonencode formatting) stable in state.
func sceneJSONValue(prior types.String, raw json.RawMessage) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	if known(prior) && sameJSON([]byte(prior.ValueString()), raw) {
		return prior
	}
	// Match Terraform jsonencode for newly read values (including exact numbers),
	// otherwise generated configuration can propose formatting-only updates.
	var value ctyjson.SimpleJSONValue
	if err := value.UnmarshalJSON(raw); err == nil {
		if canonical, err := value.MarshalJSON(); err == nil {
			return types.StringValue(string(canonical))
		}
	}
	return types.StringValue(string(raw))
}
