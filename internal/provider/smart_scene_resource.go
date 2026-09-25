package provider

import (
	"context"
	"fmt"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type smartSceneResource struct{ client *hue.Client }
type smartSceneModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Group              types.String `tfsdk:"group"`
	WeekTimeslots      types.List   `tfsdk:"week_timeslots"`
	TransitionDuration types.Int64  `tfsdk:"transition_duration"`
	State              types.String `tfsdk:"state"`
}
type smartDayModel struct {
	Recurrence types.Set  `tfsdk:"recurrence"`
	Timeslots  types.List `tfsdk:"timeslots"`
}
type smartSlotModel struct {
	StartTime types.String `tfsdk:"start_time"`
	Scene     types.String `tfsdk:"scene"`
}

var smartSlotType = types.ObjectType{AttrTypes: map[string]attr.Type{"start_time": types.StringType, "scene": types.StringType}}
var smartDayType = types.ObjectType{AttrTypes: map[string]attr.Type{"recurrence": types.SetType{ElemType: types.StringType}, "timeslots": types.ListType{ElemType: smartSlotType}}}

func (*smartSceneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_smart_scene"
}
func (*smartSceneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Version: 1, Description: "Manage a Hue smart scene's recurring schedule. Runtime activation is read-only: new smart scenes are created deactivated, and updates keep the current activation state.", Attributes: map[string]schema.Attribute{
		"id":                  schema.StringAttribute{Computed: true, Description: "Smart scene UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":                schema.StringAttribute{Required: true, Description: "Smart scene name."},
		"group":               schema.StringAttribute{Required: true, Description: "Room or zone UUID. Changing the group replaces the smart scene.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"state":               schema.StringAttribute{Computed: true, Description: "Runtime activation state: active or inactive. Never sent as a configuration update."},
		"transition_duration": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(60000), Description: "Transition duration in milliseconds. Defaults to 60000."},
		"week_timeslots": schema.ListNestedAttribute{Required: true, Description: "Ordered daily schedules. Each weekday may occur in only one schedule.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"recurrence": schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Weekdays in lowercase, such as monday or sunday."},
			"timeslots": schema.ListNestedAttribute{Required: true, Description: "Ordered scene transitions for these weekdays.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"start_time": schema.StringAttribute{Required: true, Description: "Local time as HH:MM:SS, or sunset."},
				"scene":      schema.StringAttribute{Required: true, Description: "Scene UUID. Use a hue_scene resource reference when managed."},
			}}},
		}}},
	}}
}
func (r *smartSceneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*hue.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func smartDays(ctx context.Context, value types.List) ([]hue.SmartDay, error) {
	var days []smartDayModel
	d := value.ElementsAs(ctx, &days, false)
	if d.HasError() {
		return nil, fmt.Errorf("invalid or unknown week_timeslots: %s", d.Errors()[0].Detail())
	}
	result := make([]hue.SmartDay, 0, len(days))
	for _, day := range days {
		var weekdays []string
		d = day.Recurrence.ElementsAs(ctx, &weekdays, false)
		if d.HasError() {
			return nil, fmt.Errorf("invalid recurrence")
		}
		var slots []smartSlotModel
		d = day.Timeslots.ElementsAs(ctx, &slots, false)
		if d.HasError() {
			return nil, fmt.Errorf("invalid timeslots")
		}
		entry := hue.SmartDay{Recurrence: weekdays, Timeslots: []hue.SmartSlot{}}
		for _, slot := range slots {
			if slot.Scene.IsUnknown() || slot.Scene.IsNull() || slot.StartTime.IsUnknown() || slot.StartTime.IsNull() {
				return nil, fmt.Errorf("timeslots must contain known start_time and scene values")
			}
			start, err := hue.ParseSmartStart(slot.StartTime.ValueString())
			if err != nil {
				return nil, err
			}
			entry.Timeslots = append(entry.Timeslots, hue.SmartSlot{StartTime: start, Target: hue.Reference{RID: slot.Scene.ValueString(), RType: "scene"}})
		}
		result = append(result, entry)
	}
	return result, hue.ValidateSmartSchedule(result)
}
func (*smartSceneResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m smartSceneModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if known(m.Name) && m.Name.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "name must not be empty.")
	}
	if known(m.Group) && !validUUID(m.Group.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("group"), "Invalid group", "group must be a room or zone UUID.")
	}
	if !m.TransitionDuration.IsUnknown() && !m.TransitionDuration.IsNull() && m.TransitionDuration.ValueInt64() < 0 {
		resp.Diagnostics.AddAttributeError(path.Root("transition_duration"), "Invalid transition duration", "transition_duration must not be negative.")
	}
	value, d := m.WeekTimeslots.ToTerraformValue(ctx)
	if d == nil && value.IsFullyKnown() && !value.IsNull() {
		if _, err := smartDays(ctx, m.WeekTimeslots); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("week_timeslots"), "Invalid smart scene schedule", err.Error())
		}
	}
}

type smartSceneWrite struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Group              *hue.Reference    `json:"group,omitempty"`
	WeekTimeslots      []hue.SmartDay    `json:"week_timeslots"`
	TransitionDuration int64             `json:"transition_duration"`
	Recall             *smartSceneRecall `json:"recall,omitempty"`
}
type smartSceneRecall struct {
	Action string `json:"action"`
}

func (r *smartSceneResource) body(ctx context.Context, m smartSceneModel, creating bool) (smartSceneWrite, error) {
	var body smartSceneWrite
	body.Metadata.Name = m.Name.ValueString()
	body.TransitionDuration = m.TransitionDuration.ValueInt64()
	days, err := smartDays(ctx, m.WeekTimeslots)
	if err != nil {
		return body, err
	}
	body.WeekTimeslots = days
	if creating {
		kind := "room"
		group, err := hue.GetOne[hue.Group](ctx, r.client, kind, m.Group.ValueString())
		if hue.IsNotFound(err) {
			kind = "zone"
			group, err = hue.GetOne[hue.Group](ctx, r.client, kind, m.Group.ValueString())
		}
		if err != nil {
			return body, fmt.Errorf("read smart scene group: %w", err)
		}
		for _, day := range days {
			for _, slot := range day.Timeslots {
				scene, err := hue.GetOne[hue.Scene](ctx, r.client, "scene", slot.Target.RID)
				if err != nil {
					return body, err
				}
				if scene.Group.RID != group.ID || scene.Group.RType != kind {
					return body, fmt.Errorf("scene %s does not belong to the smart scene group", scene.ID)
				}
			}
		}
		body.Group = &hue.Reference{RID: group.ID, RType: kind}
		// The bridge starts a new smart scene unless told otherwise, which would
		// immediately apply the current timeslot's scene to the lights.
		body.Recall = &smartSceneRecall{Action: "deactivate"}
	} else {
		for _, day := range days {
			for _, slot := range day.Timeslots {
				scene, err := hue.GetOne[hue.Scene](ctx, r.client, "scene", slot.Target.RID)
				if err != nil {
					return body, err
				}
				if scene.Group.RID != m.Group.ValueString() {
					return body, fmt.Errorf("scene %s does not belong to the smart scene group", scene.ID)
				}
			}
		}
	}
	return body, nil
}
func (r *smartSceneResource) refresh(ctx context.Context, m *smartSceneModel) error {
	remote, err := hue.GetOne[hue.SmartScene](ctx, r.client, "smart_scene", m.ID.ValueString())
	if err != nil {
		return err
	}
	if err = hue.ValidateSmartSchedule(remote.WeekTimeslots); err != nil {
		return err
	}
	days := []smartDayModel{}
	for _, day := range remote.WeekTimeslots {
		weekdays, d := types.SetValueFrom(ctx, types.StringType, day.Recurrence)
		if d.HasError() {
			return fmt.Errorf("invalid recurrence from bridge")
		}
		slots := []smartSlotModel{}
		for _, slot := range day.Timeslots {
			start, err := hue.FormatSmartStart(slot.StartTime)
			if err != nil {
				return err
			}
			slots = append(slots, smartSlotModel{StartTime: types.StringValue(start), Scene: types.StringValue(slot.Target.RID)})
		}
		value, d := types.ListValueFrom(ctx, smartSlotType, slots)
		if d.HasError() {
			return fmt.Errorf("invalid timeslots from bridge")
		}
		days = append(days, smartDayModel{Recurrence: weekdays, Timeslots: value})
	}
	value, d := types.ListValueFrom(ctx, smartDayType, days)
	if d.HasError() {
		return fmt.Errorf("invalid schedule from bridge")
	}
	m.WeekTimeslots = value
	m.Name = types.StringValue(remote.Metadata.Name)
	m.Group = types.StringValue(remote.Group.RID)
	m.TransitionDuration = types.Int64Value(remote.TransitionDuration)
	m.State = types.StringValue(remote.State)
	return nil
}
func (r *smartSceneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m smartSceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m, true)
	if err != nil {
		resp.Diagnostics.AddError("Invalid smart scene", err.Error())
		return
	}
	id, err := r.client.Create(ctx, "smart_scene", body)
	if err != nil {
		resp.Diagnostics.AddError("Create smart scene failed", err.Error())
		return
	}
	m.ID = types.StringValue(id)
	m.State = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if err = r.refresh(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Read created smart scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *smartSceneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m smartSceneModel
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
		resp.Diagnostics.AddError("Read smart scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *smartSceneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m smartSceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m, false)
	if err == nil {
		err = r.client.Update(ctx, "smart_scene", m.ID.ValueString(), body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Update smart scene failed", err.Error())
		return
	}
	if err = r.refresh(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Read updated smart scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *smartSceneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m smartSceneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, "smart_scene", m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete smart scene failed", err.Error())
	}
}
func (*smartSceneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
}
