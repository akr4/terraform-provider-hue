package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool { return uuidPattern.MatchString(s) }

type groupResource struct {
	kind, childKind string
	client          *hue.Client
}
type groupModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Archetype types.String `tfsdk:"archetype"`
	Children  types.Set    `tfsdk:"children"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind
}
func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manage a Hue " + r.kind + ". Deleting this resource deletes the group from the bridge.", Attributes: map[string]schema.Attribute{
		"id":        schema.StringAttribute{Computed: true, Description: "Bridge resource UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":      schema.StringAttribute{Required: true, Description: "Group name."},
		"archetype": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("other"), Description: "Hue group archetype. Defaults to other."},
		"children":  schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Set of " + r.childKind + " UUIDs."},
	}}
}
func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*hue.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func (r *groupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Name.IsUnknown() && !m.Name.IsNull() && m.Name.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "name must not be empty.")
	}
	for _, child := range m.Children.Elements() {
		s := child.(types.String)
		if !s.IsUnknown() && (s.IsNull() || !validUUID(s.ValueString())) {
			resp.Diagnostics.AddAttributeError(path.Root("children"), "Invalid child ID", "Each child must be a UUID.")
		}
	}
}
func (r *groupResource) body(ctx context.Context, m groupModel) (hue.Group, error) {
	var children []string
	diags := m.Children.ElementsAs(ctx, &children, false)
	if diags.HasError() {
		return hue.Group{}, fmt.Errorf("invalid children: %s", diags.Errors()[0].Detail())
	}
	refs := make([]hue.Reference, 0, len(children))
	for _, id := range children {
		refs = append(refs, hue.Reference{RID: id, RType: r.childKind})
	}
	return hue.Group{Metadata: hue.Metadata{Name: m.Name.ValueString(), Archetype: m.Archetype.ValueString()}, Children: refs}, nil
}
func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid group", err.Error())
		return
	}
	id, err := r.client.Create(ctx, r.kind, body)
	if err != nil {
		resp.Diagnostics.AddError("Create group failed", err.Error())
		return
	}
	m.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := hue.GetOne[hue.Group](ctx, r.client, r.kind, m.ID.ValueString())
	if hue.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read group failed", err.Error())
		return
	}
	m.Name = types.StringValue(group.Metadata.Name)
	m.Archetype = types.StringValue(group.Metadata.Archetype)
	children := []string{}
	for _, child := range group.Children {
		children = append(children, child.RID)
	}
	value, d := types.SetValueFrom(ctx, types.StringType, children)
	resp.Diagnostics.Append(d...)
	m.Children = value
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.body(ctx, m)
	if err == nil {
		err = r.client.Update(ctx, r.kind, m.ID.ValueString(), body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Update group failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, r.kind, m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete group failed", err.Error())
	}
}
func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
}
func importID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validUUID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a bridge resource UUID.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
