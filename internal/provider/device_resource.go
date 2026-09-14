package provider

import (
	"context"
	"unicode/utf8"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type deviceResource struct{ client *hue.Client }
type deviceModel struct {
	ID        types.String `tfsdk:"id"`
	DeviceID  types.String `tfsdk:"device_id"`
	Name      types.String `tfsdk:"name"`
	Archetype types.String `tfsdk:"archetype"`
	LightIDs  types.Set    `tfsdk:"light_ids"`
}

func (*deviceResource) Metadata(_ context.Context, _ resource.MetadataRequest, r *resource.MetadataResponse) {
	r.TypeName = "hue_device"
}
func (*deviceResource) Schema(_ context.Context, _ resource.SchemaRequest, r *resource.SchemaResponse) {
	r.Schema = schema.Schema{Description: "Manage the name and archetype of an already paired device. Creation adopts an existing device by UUID; it never pairs hardware. Destroy only removes Terraform management, leaving the device and its settings unchanged.", Attributes: map[string]schema.Attribute{
		"light_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "UUIDs of light services owned by this device, suitable for scene actions and zone children. Empty for devices without lights. Use one(light_ids) only for devices with exactly one light service."},
		"id":        schema.StringAttribute{Computed: true, Description: "Device UUID."},
		"device_id": schema.StringAttribute{Required: true, Description: "UUID of an already paired device, not a light service UUID. Changes replace only the Terraform management binding.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"name":      schema.StringAttribute{Optional: true, Computed: true, Description: "Device name (1–32 characters). Omitted names are preserved."},
		"archetype": schema.StringAttribute{Optional: true, Computed: true, Description: "Device product archetype used for its icon. Omitted values are preserved; supported values depend on the Bridge."},
	}}
}
func (r *deviceResource) Configure(_ context.Context, q resource.ConfigureRequest, s *resource.ConfigureResponse) {
	if q.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = q.ProviderData.(*hue.Client)
	if !ok {
		s.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func (*deviceResource) ValidateConfig(ctx context.Context, q resource.ValidateConfigRequest, s *resource.ValidateConfigResponse) {
	var m deviceModel
	s.Diagnostics.Append(q.Config.Get(ctx, &m)...)
	if s.Diagnostics.HasError() {
		return
	}
	if known(m.DeviceID) && !validUUID(m.DeviceID.ValueString()) {
		s.Diagnostics.AddAttributeError(path.Root("device_id"), "Invalid device ID", "device_id must be a UUID.")
	}
	if known(m.Name) && (utf8.RuneCountInString(m.Name.ValueString()) < 1 || utf8.RuneCountInString(m.Name.ValueString()) > 32) {
		s.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "name must contain 1–32 characters.")
	}
	if known(m.Archetype) && m.Archetype.ValueString() == "" {
		s.Diagnostics.AddAttributeError(path.Root("archetype"), "Invalid archetype", "archetype must not be empty.")
	}
}
func deviceMetadata(config deviceModel) map[string]string {
	fields := map[string]string{}
	if known(config.Name) {
		fields["name"] = config.Name.ValueString()
	}
	if known(config.Archetype) {
		fields["archetype"] = config.Archetype.ValueString()
	}
	return fields
}
func deviceLightIDs(d hue.Device) types.Set {
	ids := []attr.Value{}
	for _, service := range d.Services {
		if service.RType == "light" {
			ids = append(ids, types.StringValue(service.RID))
		}
	}
	return types.SetValueMust(types.StringType, ids)
}
func deviceState(m *deviceModel, d hue.Device) {
	m.LightIDs = deviceLightIDs(d)
	m.ID = types.StringValue(d.ID)
	m.DeviceID = m.ID
	m.Name = types.StringValue(d.Metadata.Name)
	m.Archetype = types.StringNull()
	if d.Metadata.Archetype != "" {
		m.Archetype = types.StringValue(d.Metadata.Archetype)
	}
}
func (r *deviceResource) Create(ctx context.Context, q resource.CreateRequest, s *resource.CreateResponse) {
	var m, c deviceModel
	s.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	s.Diagnostics.Append(q.Config.Get(ctx, &c)...)
	if s.Diagnostics.HasError() {
		return
	}
	d, err := hue.GetOne[hue.Device](ctx, r.client, "device", m.DeviceID.ValueString())
	if err != nil {
		s.Diagnostics.AddError("Read existing device failed", err.Error())
		return
	}
	fields := deviceMetadata(c)
	if len(fields) > 0 {
		if err = r.client.Update(ctx, "device", d.ID, map[string]any{"metadata": fields}); err != nil {
			s.Diagnostics.AddError("Update device failed", err.Error())
			return
		}
	}
	// Record the binding after a successful write, even if readback fails.
	m.LightIDs = deviceLightIDs(d)
	m.ID = types.StringValue(d.ID)
	if c.Name.IsNull() {
		m.Name = types.StringValue(d.Metadata.Name)
	}
	if c.Archetype.IsNull() {
		m.Archetype = types.StringNull()
		if d.Metadata.Archetype != "" {
			m.Archetype = types.StringValue(d.Metadata.Archetype)
		}
	}
	s.Diagnostics.Append(s.State.Set(ctx, &m)...)
	d, err = hue.GetOne[hue.Device](ctx, r.client, "device", d.ID)
	if err != nil {
		s.Diagnostics.AddError("Read configured device failed", err.Error())
		return
	}
	deviceState(&m, d)
	s.Diagnostics.Append(s.State.Set(ctx, &m)...)
}
func (r *deviceResource) Read(ctx context.Context, q resource.ReadRequest, s *resource.ReadResponse) {
	var m deviceModel
	s.Diagnostics.Append(q.State.Get(ctx, &m)...)
	if s.Diagnostics.HasError() {
		return
	}
	d, err := hue.GetOne[hue.Device](ctx, r.client, "device", m.ID.ValueString())
	if hue.IsNotFound(err) {
		s.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		s.Diagnostics.AddError("Read device failed", err.Error())
		return
	}
	deviceState(&m, d)
	s.Diagnostics.Append(s.State.Set(ctx, &m)...)
}
func (r *deviceResource) Update(ctx context.Context, q resource.UpdateRequest, s *resource.UpdateResponse) {
	var m, c deviceModel
	s.Diagnostics.Append(q.Plan.Get(ctx, &m)...)
	s.Diagnostics.Append(q.Config.Get(ctx, &c)...)
	if s.Diagnostics.HasError() {
		return
	}
	fields := deviceMetadata(c)
	if len(fields) > 0 {
		if err := r.client.Update(ctx, "device", m.DeviceID.ValueString(), map[string]any{"metadata": fields}); err != nil {
			s.Diagnostics.AddError("Update device failed", err.Error())
			return
		}
	}
	d, err := hue.GetOne[hue.Device](ctx, r.client, "device", m.DeviceID.ValueString())
	if err != nil {
		s.Diagnostics.AddError("Read configured device failed", err.Error())
		return
	}
	deviceState(&m, d)
	s.Diagnostics.Append(s.State.Set(ctx, &m)...)
}
func (*deviceResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}
func (*deviceResource) ImportState(ctx context.Context, q resource.ImportStateRequest, s *resource.ImportStateResponse) {
	if !validUUID(q.ID) {
		s.Diagnostics.AddError("Invalid import ID", "Import requires a device UUID.")
		return
	}
	s.Diagnostics.Append(s.State.SetAttribute(ctx, path.Root("id"), q.ID)...)
	s.Diagnostics.Append(s.State.SetAttribute(ctx, path.Root("device_id"), q.ID)...)
}

// Metadata changes do not change the binding or its light service identifiers.
// Preserve these references during planning, but never carry them to a different device.
func (*deviceResource) ModifyPlan(ctx context.Context, q resource.ModifyPlanRequest, s *resource.ModifyPlanResponse) {
	if q.Plan.Raw.IsNull() {
		return
	}
	var plan deviceModel
	s.Diagnostics.Append(q.Plan.Get(ctx, &plan)...)
	if s.Diagnostics.HasError() || !known(plan.DeviceID) {
		return
	}
	s.Diagnostics.Append(s.Plan.SetAttribute(ctx, path.Root("id"), plan.DeviceID)...)
	if q.State.Raw.IsNull() {
		return
	}
	var state deviceModel
	s.Diagnostics.Append(q.State.Get(ctx, &state)...)
	if s.Diagnostics.HasError() {
		return
	}
	if plan.DeviceID.Equal(state.DeviceID) && plan.LightIDs.IsUnknown() && !state.LightIDs.IsNull() && !state.LightIDs.IsUnknown() {
		s.Diagnostics.Append(s.Plan.SetAttribute(ctx, path.Root("light_ids"), state.LightIDs)...)
	}
}
