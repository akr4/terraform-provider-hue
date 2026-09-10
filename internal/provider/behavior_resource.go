package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

type behaviorResource struct{ client *hue.Client }
type behaviorModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	ScriptID      types.String `tfsdk:"script_id"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	Configuration types.String `tfsdk:"configuration"`
	Status        types.String `tfsdk:"status"`
	LastError     types.String `tfsdk:"last_error"`
}

func (*behaviorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_behavior_instance"
}
func (*behaviorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manage Hue behavior instances, including switch button and rotary assignments. Creation requires script_id. Deleting this resource deletes the assignment from the bridge, not the physical device.", Attributes: map[string]schema.Attribute{
		"id":            schema.StringAttribute{Computed: true, Description: "Behavior instance UUID (not the switch device UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":          schema.StringAttribute{Required: true, Description: "Behavior instance name; does not rename the physical device."},
		"script_id":     schema.StringAttribute{Optional: true, Computed: true, Description: "Behavior script UUID. Required for creation, optional for imported instances. Changing it replaces the behavior.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}},
		"enabled":       schema.BoolAttribute{Required: true, Description: "Enable or disable this behavior."},
		"configuration": schema.StringAttribute{Required: true, Description: "Complete script-specific JSON configuration. Use jsonencode({...}) and resource references for scene/group IDs. All configuration fields must be preserved."},
		"status":        schema.StringAttribute{Computed: true, Description: "Runtime status reported by the bridge."},
		"last_error":    schema.StringAttribute{Computed: true, Description: "Last runtime error reported by the bridge."},
	}}
}
func (r *behaviorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*hue.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func (*behaviorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m behaviorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() && m.Name.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "name must not be empty.")
	}
	if !m.ScriptID.IsNull() && !m.ScriptID.IsUnknown() && !validUUID(m.ScriptID.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("script_id"), "Invalid script ID", "script_id must be a behavior script UUID.")
	}

	if !m.Configuration.IsNull() && !m.Configuration.IsUnknown() {
		if err := hue.ValidateConfiguration([]byte(m.Configuration.ValueString())); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("configuration"), "Invalid configuration", err.Error())
		}
	}
}

// ModifyPlan gives a useful error before apply when a new behavior has no script.
func (*behaviorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if !req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var script types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("script_id"), &script)...)
	if script.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("script_id"), "Script ID required for creation", "Set script_id to an existing behavior script UUID, or import an existing behavior instance.")
	}
}

type behaviorWrite struct {
	Type     string `json:"type,omitempty"`
	ScriptID string `json:"script_id,omitempty"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Enabled       bool            `json:"enabled"`
	Configuration json.RawMessage `json:"configuration"`
}

func behaviorBody(m behaviorModel) (behaviorWrite, error) {
	body := behaviorWrite{Enabled: m.Enabled.ValueBool(), Configuration: json.RawMessage(m.Configuration.ValueString())}
	body.Metadata.Name = m.Name.ValueString()
	return body, hue.ValidateConfiguration(body.Configuration)
}
func (r *behaviorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m behaviorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.ScriptID.IsNull() || m.ScriptID.IsUnknown() || !validUUID(m.ScriptID.ValueString()) {
		resp.Diagnostics.AddError("Script ID required for creation", "Set script_id to an existing behavior script UUID.")
		return
	}
	body, err := behaviorBody(m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	if err = r.checkDuplicateSwitch(ctx, body.Configuration); err != nil {
		resp.Diagnostics.AddError("Cannot create switch assignment", err.Error())
		return
	}
	body.Type = "behavior_instance"
	body.ScriptID = m.ScriptID.ValueString()
	id, err := r.client.Create(ctx, "behavior_instance", body)
	if err != nil {
		resp.Diagnostics.AddError("Create behavior failed", err.Error())
		return
	}
	// Keep the returned ID even if the subsequent read fails, so it can be
	// recovered/refreshed instead of losing track of an existing assignment.
	m.ID = types.StringValue(id)
	m.Status = types.StringNull()
	m.LastError = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	b, err := hue.GetOne[hue.BehaviorInstance](ctx, r.client, "behavior_instance", id)
	if err != nil {
		resp.Diagnostics.AddError("Read created behavior failed", err.Error())
		return
	}
	m.Status = types.StringValue(b.Status)
	m.LastError = types.StringValue(b.LastError)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *behaviorResource) checkDuplicateSwitch(ctx context.Context, raw json.RawMessage) error {
	var config struct {
		Device hue.Reference `json:"device"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	if config.Device.RID == "" || config.Device.RType != "device" {
		return nil
	}
	var behaviors []hue.BehaviorInstance
	if err := r.client.Get(ctx, "/clip/v2/resource/behavior_instance", &behaviors); err != nil {
		return err
	}
	for _, b := range behaviors {
		var other struct {
			Device hue.Reference `json:"device"`
		}
		if err := json.Unmarshal(b.Configuration, &other); err != nil {
			return err
		}
		if other.Device == config.Device {
			return fmt.Errorf("device %s already has behavior %s; import that behavior instead", config.Device.RID, b.ID)
		}
	}
	return nil
}
func (r *behaviorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m behaviorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, err := hue.GetOne[hue.BehaviorInstance](ctx, r.client, "behavior_instance", m.ID.ValueString())
	if hue.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read behavior failed", err.Error())
		return
	}
	if err = hue.ValidateConfiguration(b.Configuration); err != nil {
		resp.Diagnostics.AddError("Invalid bridge configuration", err.Error())
		return
	}
	m.Name = types.StringValue(b.Metadata.Name)
	m.ScriptID = types.StringValue(b.ScriptID)
	m.Enabled = types.BoolValue(b.Enabled)
	// Keep the prior representation if only JSON whitespace, object order or
	// number formatting changed; real values (including array order) still drift.
	if m.Configuration.IsNull() || m.Configuration.IsUnknown() || !sameJSON([]byte(m.Configuration.ValueString()), b.Configuration) {
		ty, _ := ctyjson.ImpliedType(b.Configuration)
		v, err := ctyjson.Unmarshal(b.Configuration, ty)
		if err != nil {
			resp.Diagnostics.AddError("Invalid bridge configuration", err.Error())
			return
		}
		raw, err := ctyjson.Marshal(v, ty)
		if err != nil {
			resp.Diagnostics.AddError("Invalid bridge configuration", err.Error())
			return
		}
		m.Configuration = types.StringValue(string(raw))
	}
	m.Status = types.StringValue(b.Status)
	m.LastError = types.StringValue(b.LastError)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func sameJSON(a, b []byte) bool {
	ta, ea := ctyjson.ImpliedType(a)
	tb, eb := ctyjson.ImpliedType(b)
	if ea != nil || eb != nil {
		return false
	}
	va, ea := ctyjson.Unmarshal(a, ta)
	vb, eb := ctyjson.Unmarshal(b, tb)
	return ea == nil && eb == nil && va.RawEquals(vb)
}
func (r *behaviorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m behaviorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := behaviorBody(m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	if err := r.client.Update(ctx, "behavior_instance", m.ID.ValueString(), body); err != nil {
		resp.Diagnostics.AddError("Update behavior failed", err.Error())
		return
	}
	// The runtime fields may change asynchronously after configuration updates.
	b, err := hue.GetOne[hue.BehaviorInstance](ctx, r.client, "behavior_instance", m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read updated behavior failed", err.Error())
		return
	}
	m.Status = types.StringValue(b.Status)
	m.LastError = types.StringValue(b.LastError)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *behaviorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m behaviorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, "behavior_instance", m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete behavior failed", err.Error())
	}
}
func (*behaviorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
}
