package provider

import (
	"context"
	"encoding/json"

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
	resp.Schema = schema.Schema{Description: "Manage an existing Hue behavior instance, including switch button and rotary assignments. Import an instance created in the Hue app. Creation and deletion on the bridge are not supported; use a removed block with destroy = false to stop managing it.", Attributes: map[string]schema.Attribute{
		"id":            schema.StringAttribute{Computed: true, Description: "Behavior instance UUID (not the switch device UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":          schema.StringAttribute{Required: true, Description: "Behavior instance name; does not rename the physical device."},
		"script_id":     schema.StringAttribute{Computed: true, Description: "Read-only behavior script UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
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
	if !m.Configuration.IsNull() && !m.Configuration.IsUnknown() {
		if err := hue.ValidateConfiguration([]byte(m.Configuration.ValueString())); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("configuration"), "Invalid configuration", err.Error())
		}
	}
}
func (*behaviorResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Import required", "Create the switch assignment in the Hue app, then terraform import the existing behavior instance UUID. This resource does not create or pair switches.")
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
	if err := hue.ValidateConfiguration([]byte(m.Configuration.ValueString())); err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	body := struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Enabled       bool            `json:"enabled"`
		Configuration json.RawMessage `json:"configuration"`
	}{Enabled: m.Enabled.ValueBool(), Configuration: json.RawMessage(m.Configuration.ValueString())}
	body.Metadata.Name = m.Name.ValueString()
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
func (*behaviorResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError("Behavior deletion is not supported", "To disable this assignment, set enabled = false and apply. To stop managing it, use a removed block with lifecycle { destroy = false }, or terraform state rm. Delete the assignment itself in the Hue app.")
}
func (*behaviorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
}
