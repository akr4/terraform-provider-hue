package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

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
	ImageID     types.String  `tfsdk:"image_id"`
	Palette     types.String  `tfsdk:"palette"`
}
type actionModel struct {
	Gradient   types.String  `tfsdk:"gradient"`
	Effects    types.String  `tfsdk:"effects"`
	On         types.Bool    `tfsdk:"on"`
	Brightness types.Float64 `tfsdk:"brightness"`
	Mirek      types.Int64   `tfsdk:"mirek"`
	Kelvin     types.Int64   `tfsdk:"kelvin"`
	XY         types.Object  `tfsdk:"color_xy"`
}

var xyTypes = map[string]attr.Type{"x": types.Float64Type, "y": types.Float64Type}
var actionTypes = map[string]attr.Type{"gradient": types.StringType, "effects": types.StringType, "on": types.BoolType, "brightness": types.Float64Type, "mirek": types.Int64Type, "kelvin": types.Int64Type, "color_xy": types.ObjectType{AttrTypes: xyTypes}}
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
func emptyAction() actionModel {
	return actionModel{Gradient: types.StringNull(), Effects: types.StringNull(), On: types.BoolNull(), Brightness: types.Float64Null(), Mirek: types.Int64Null(), Kelvin: types.Int64Null(), XY: types.ObjectNull(xyTypes)}
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
	resp.Schema = schema.Schema{Version: 1, Description: "Manage a Hue scene. Changing its group replaces it. Palette is read-only in v0.", Attributes: map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Bridge resource UUID."},
		"name":         schema.StringAttribute{Required: true, Description: "Scene name."},
		"group":        schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Room or zone UUID. Changes replace the scene."},
		"speed":        schema.Float64Attribute{Optional: true, Computed: true, Description: "Dynamic scene speed from 0 to 1."},
		"auto_dynamic": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Enable dynamic playback. Defaults to false."},
		"image_id":     schema.StringAttribute{Optional: true, Computed: true, Description: "Image resource UUID. Preserved on import."},
		"palette":      schema.StringAttribute{Computed: true, Description: "Read-only palette as canonical JSON. Never sent in create or update requests."},
		"actions": schema.MapNestedAttribute{Required: true, Description: "Actions keyed by light UUID.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"gradient":   schema.StringAttribute{Optional: true, Computed: true, Description: "Gradient action as a JSON object; use jsonencode. Preserved from the bridge when omitted."},
			"effects":    schema.StringAttribute{Optional: true, Computed: true, Description: "Effect action as a JSON object; use jsonencode. Preserved from the bridge when omitted."},
			"on":         schema.BoolAttribute{Optional: true, Description: "On/off state."},
			"brightness": schema.Float64Attribute{Optional: true, Description: "Brightness from 0 to 100."},
			"mirek":      schema.Int64Attribute{Optional: true, Computed: true, Description: "Color temperature, 153–500. Conflicts with kelvin; may omit both."},
			"kelvin":     schema.Int64Attribute{Optional: true, Computed: true, Description: "Positive color temperature in kelvin. Conflicts with mirek."},
			"color_xy":   schema.SingleNestedAttribute{Optional: true, Computed: true, Description: "CIE xy chromaticity. Brightness is configured separately.", Attributes: map[string]schema.Attribute{"x": schema.Float64Attribute{Required: true}, "y": schema.Float64Attribute{Required: true}}},
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
	for name, value := range map[string]types.String{"group": m.Group, "image_id": m.ImageID} {
		if known(value) && !validUUID(value.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid UUID", name+" must be a UUID.")
		}
	}
	if known(m.Speed) && (m.Speed.ValueFloat64() < 0 || m.Speed.ValueFloat64() > 1) {
		resp.Diagnostics.AddAttributeError(path.Root("speed"), "Invalid speed", "speed must be between 0 and 1.")
	}
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
		resp.Diagnostics.Append(value.(types.Object).As(ctx, &a, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			continue
		}
		for _, message := range validateAction(a) {
			resp.Diagnostics.AddAttributeError(p, "Invalid action", message)
		}
	}
}
func validateAction(a actionModel) []string {
	var errs []string
	for key, value := range map[string]types.String{"gradient": a.Gradient, "effects": a.Effects} {
		if known(value) {
			if err := hue.ValidateConfiguration([]byte(value.ValueString())); err != nil {
				errs = append(errs, key+" must be a JSON object.")
			}
		}
	}
	if !a.Mirek.IsNull() && !a.Kelvin.IsNull() {
		errs = append(errs, "mirek and kelvin cannot both be configured.")
	}
	if known(a.Mirek) && (a.Mirek.ValueInt64() < 153 || a.Mirek.ValueInt64() > 500) {
		errs = append(errs, "mirek must be between 153 and 500.")
	}
	if known(a.Kelvin) && a.Kelvin.ValueInt64() <= 0 {
		errs = append(errs, "kelvin must be positive.")
	}
	if known(a.Brightness) && (a.Brightness.ValueFloat64() < 0 || a.Brightness.ValueFloat64() > 100) {
		errs = append(errs, "brightness must be between 0 and 100.")
	}
	if p, ok := readXY(a.XY); ok && (p.X < 0 || p.Y < 0 || p.X > 1 || p.Y > 1 || p.X+p.Y > 1+1e-12 || math.IsNaN(p.X) || math.IsNaN(p.Y)) {
		errs = append(errs, "color_xy must have x >= 0, y >= 0 and x + y <= 1.")
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
	if known(m.ImageID) {
		scene.Metadata.Image = &hue.Reference{RID: m.ImageID.ValueString(), RType: "public_image"}
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
		for field, value := range map[string]types.String{"gradient": a.Gradient, "effects": a.Effects} {
			if !known(value) {
				continue
			}
			raw := json.RawMessage(value.ValueString())
			if err := hue.ValidateConfiguration(raw); err != nil {
				return scene, fmt.Errorf("invalid %s action: %w", field, err)
			}
			if field == "gradient" {
				action.Gradient = raw
			} else {
				action.Effects = raw
			}
		}
		if known(a.On) {
			action.On = &hue.On{On: a.On.ValueBool()}
		}
		if known(a.Brightness) {
			action.Dimming = &hue.Dimming{Brightness: a.Brightness.ValueFloat64()}
		}
		// Only configured color representations select writes. Computed counterparts
		// from prior state must never override a newly configured value or removal.
		if known(c.Mirek) {
			action.ColorTemperature = &hue.Temperature{Mirek: c.Mirek.ValueInt64()}
		} else if known(c.Kelvin) {
			action.ColorTemperature = &hue.Temperature{Mirek: colors.ClampMirek(colors.KelvinToMirek(c.Kelvin.ValueInt64()), 153, 500)}
		}
		if p, ok := readXY(c.XY); ok {
			action.Color = &hue.ActionColor{XY: colors.Round(p)}
		}
		scene.Actions = append(scene.Actions, hue.SceneAction{Target: hue.Reference{RID: id, RType: "light"}, Action: action})
	}
	return scene, nil
}

// reconcileAction keeps the user's exact representation when the bridge value
// is equivalent in xy/mirek space, including clipping to hardware capabilities.
func reconcileAction(prior actionModel, actual hue.Action, light hue.Light) actionModel {
	next := emptyAction()
	next.Gradient = sceneJSONValue(prior.Gradient, actual.Gradient)
	next.Effects = sceneJSONValue(prior.Effects, actual.Effects)
	if actual.On != nil {
		next.On = types.BoolValue(actual.On.On)
	}
	if actual.Dimming != nil {
		next.Brightness = types.Float64Value(actual.Dimming.Brightness)
	}
	if actual.Color != nil {
		gamut := (*hue.Gamut)(nil)
		if light.Color != nil {
			gamut = colors.Gamut(light.Color.GamutType)
			if gamut == nil {
				gamut = light.Color.Gamut
			}
		}
		next.XY = xyValue(actual.Color.XY)
		expected, ok := readXY(prior.XY)
		if ok && colors.EqualXY(expected, actual.Color.XY, gamut) {
			if known(prior.XY) {
				next.XY = prior.XY
			}
		}
	}
	if actual.ColorTemperature != nil {
		m := actual.ColorTemperature.Mirek
		next.Mirek = types.Int64Value(m)
		next.Kelvin = types.Int64Value(colors.MirekToKelvin(m))
		expected, ok := prior.Mirek.ValueInt64(), known(prior.Mirek)
		if !ok && known(prior.Kelvin) {
			expected = colors.KelvinToMirek(prior.Kelvin.ValueInt64())
			ok = true
		}
		min, max := int64(153), int64(500)
		if light.ColorTemperature != nil {
			min, max = light.ColorTemperature.MirekSchema.Min, light.ColorTemperature.MirekSchema.Max
		}
		if ok && colors.EqualMirek(expected, m, min, max) {
			if known(prior.Mirek) {
				next.Mirek = prior.Mirek
			}
			if known(prior.Kelvin) {
				next.Kelvin = prior.Kelvin
			}
		}
	}
	return next
}
func (r *sceneResource) refresh(ctx context.Context, m *sceneModel) error {
	scene, err := hue.GetOne[hue.Scene](ctx, r.client, "scene", m.ID.ValueString())
	if err != nil {
		return err
	}
	byID := map[string]hue.Light{}
	if len(scene.Actions) > 0 {
		ids := make([]string, 0, len(scene.Actions))
		for _, item := range scene.Actions {
			ids = append(ids, item.Target.RID)
		}
		byID, err = r.client.LightCapabilities(ctx, ids)
		if err != nil {
			return fmt.Errorf("read light capabilities: %w", err)
		}
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
		light, ok := byID[item.Target.RID]
		if !ok {
			return fmt.Errorf("scene references missing light %s", item.Target.RID)
		}
		actions[item.Target.RID] = reconcileAction(old, item.Action, light)
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
	m.ImageID = types.StringNull()
	if scene.Metadata.Image != nil {
		m.ImageID = types.StringValue(scene.Metadata.Image.RID)
	}
	m.Palette = types.StringNull()
	if len(scene.Palette) > 0 && string(scene.Palette) != "null" {
		var value any
		if err = json.Unmarshal(scene.Palette, &value); err != nil {
			return err
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return err
		}
		m.Palette = types.StringValue(string(canonical))
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
	var m, config, prior sceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m, config, false)
	if err == nil {
		// Some app-created scenes reject metadata.image even when its value is
		// unchanged. Preserve imported images without resending them on edits.
		if m.ImageID.Equal(prior.ImageID) {
			body.Metadata.Image = nil
		}
		// group is immutable and must not be included in PUT; palette is read-only.
		payload := map[string]any{"metadata": body.Metadata, "actions": body.Actions}
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

// ModifyPlan resolves computed temperature counterparts without bridge access.
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
		if config.ImageID.IsNull() {
			planned.ImageID = prior.ImageID
		}
		if config.Speed.IsNull() {
			planned.Speed = prior.Speed
		}
		planned.Palette = prior.Palette
	}
	if known(config.Actions) && known(planned.Actions) {
		for _, value := range config.Actions.Elements() {
			if !known(value) {
				return
			}
		}
		for _, value := range planned.Actions.Elements() {
			if !known(value) {
				return
			}
		}
		configured, d := actionsFrom(ctx, config.Actions)
		resp.Diagnostics.Append(d...)
		actions, d := actionsFrom(ctx, planned.Actions)
		resp.Diagnostics.Append(d...)
		oldActions := map[string]actionModel{}
		if !req.State.Raw.IsNull() {
			oldActions, d = actionsFrom(ctx, prior.Actions)
			resp.Diagnostics.Append(d...)
		}
		if resp.Diagnostics.HasError() {
			return
		}
		for id, a := range actions {
			c := configured[id]
			old, ok := oldActions[id]
			if !ok {
				old = emptyAction()
			}
			a = planAction(c, a, old)
			actions[id] = a
		}
		planned.Actions, d = types.MapValueFrom(ctx, actionType, actions)
		resp.Diagnostics.Append(d...)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &planned)...)
}
func planAction(config, planned, prior actionModel) actionModel {
	if config.XY.IsNull() {
		planned.XY = types.ObjectNull(xyTypes)
	}
	if config.Gradient.IsNull() {
		planned.Gradient = prior.Gradient
	}
	if config.Effects.IsNull() {
		planned.Effects = prior.Effects
	}
	if config.Mirek.IsNull() && config.Kelvin.IsNull() {
		planned.Mirek = types.Int64Null()
		planned.Kelvin = types.Int64Null()
	} else if !config.Mirek.IsNull() {
		planned.Kelvin = types.Int64Unknown()
		if known(config.Mirek) && config.Mirek.Equal(prior.Mirek) {
			planned.Kelvin = prior.Kelvin
		}
	} else {
		planned.Mirek = types.Int64Unknown()
		if known(config.Kelvin) && config.Kelvin.Equal(prior.Kelvin) {
			planned.Mirek = prior.Mirek
		}
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
	return types.StringValue(string(raw))
}
