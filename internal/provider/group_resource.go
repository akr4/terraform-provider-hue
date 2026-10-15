package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Archetype    types.String `tfsdk:"archetype"`
	Children     types.Set    `tfsdk:"children"`
	ForceDestroy types.Bool   `tfsdk:"force_destroy"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind
}
func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	children := "Set of " + r.childKind + " UUIDs."
	if r.kind == "room" {
		// Verified on a bridge: the device leaves its old room, and that room's
		// scenes drop the actions for its lights.
		children += " A device belongs to one room, so adding a device that is in another room moves it here and removes its lights from that room's scenes."
	}
	resp.Schema = schema.Schema{Description: "Manage a Hue " + r.kind + ". Deleting this resource deletes the " + r.kind + " from the bridge, and fails while scenes remain in it unless `force_destroy` is true.", Attributes: map[string]schema.Attribute{
		"id":            schema.StringAttribute{Computed: true, Description: "Bridge resource UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":          schema.StringAttribute{Required: true, Description: "Group name."},
		"archetype":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("other"), Description: "Hue group archetype. Defaults to other."},
		"children":      schema.SetAttribute{Required: true, ElementType: types.StringType, Description: children},
		"force_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "The bridge deletes a " + r.kind + "'s scenes along with it, including scenes created in the Hue app. Unless this is true, deleting the " + r.kind + " fails while any scene or smart scene remains in it. Apply the change before deleting the " + r.kind + ". Defaults to false."},
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
	// force_destroy exists only in Terraform; keep it, and default it after import.
	if m.ForceDestroy.IsNull() {
		m.ForceDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A force_destroy change alone has nothing to send to the bridge.
	if !m.Name.Equal(prior.Name) || !m.Archetype.Equal(prior.Archetype) || !m.Children.Equal(prior.Children) {
		body, err := r.body(ctx, m)
		if err == nil {
			err = r.client.Update(ctx, r.kind, m.ID.ValueString(), body)
		}
		if err != nil {
			resp.Diagnostics.AddError("Update group failed", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// A physical bridge deletes a room's or zone's scenes with it, which the plan
// cannot show. Scenes that reference the group in Terraform are deleted first,
// so anything left here is unmanaged; refuse like an S3 bucket's force_destroy.
// A scene with a hard-coded group UUID has no such order and may be deleted
// in the same run, in which case running the command again succeeds.
const scenesRemainDetail = `The bridge deletes a %[1]s's scenes along with it, including scenes created in the Hue app. These remain in this %[1]s:
  - %[2]s

Delete them first, or set force_destroy = true and apply that change before deleting the %[1]s. Deletion uses the value saved in state, so changing the configuration alone is not enough.

If Terraform is also deleting these scenes, run the same command again.`

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.ForceDestroy.ValueBool() {
		scenes, err := r.scenesIn(ctx, m.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Delete group failed", "Could not check for scenes in the "+r.kind+": "+err.Error())
			return
		}
		if len(scenes) > 0 {
			resp.Diagnostics.AddError(strings.ToUpper(r.kind[:1])+r.kind[1:]+" still contains scenes", fmt.Sprintf(scenesRemainDetail, r.kind, strings.Join(scenes, "\n  - ")))
			return
		}
	}
	if err := r.client.Delete(ctx, r.kind, m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete group failed", err.Error())
	}
}

// scenesIn returns the sorted names of the scenes and smart scenes in a group.
func (r *groupResource) scenesIn(ctx context.Context, id string) ([]string, error) {
	var names []string
	for _, kind := range []string{"scene", "smart_scene"} {
		var items []struct {
			Metadata hue.Metadata  `json:"metadata"`
			Group    hue.Reference `json:"group"`
		}
		if err := r.client.Get(ctx, "/clip/v2/resource/"+kind, &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Group.RID != id {
				continue
			}
			name := fmt.Sprintf("%q", item.Metadata.Name)
			if kind == "smart_scene" {
				name += " (smart scene)"
			}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID(ctx, req, resp)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("force_destroy"), false)...)
	}
}
func importID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validUUID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a bridge resource UUID.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
