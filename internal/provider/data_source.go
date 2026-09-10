package provider

import (
	"context"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type lookupDataSource struct {
	kind   string
	client *hue.Client
}

func (d *lookupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.kind
}
func (d *lookupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{"id": schema.StringAttribute{Required: true, Description: "Resource UUID."}, "name": schema.StringAttribute{Computed: true, Description: "Resource name."}}
	if d.kind == "light" {
		attrs["device_id"] = schema.StringAttribute{Computed: true, Description: "Owner device UUID, suitable for room children."}
		attrs["gamut_type"] = schema.StringAttribute{Computed: true, Description: "A, B, C, or other; null for a light without color."}
		for _, key := range []string{"supports_color", "supports_color_temperature"} {
			attrs[key] = schema.BoolAttribute{Computed: true}
		}
		for _, key := range []string{"mirek_min", "mirek_max"} {
			attrs[key] = schema.Int64Attribute{Computed: true}
		}
	} else {
		attrs["model_id"] = schema.StringAttribute{Computed: true}
		attrs["light_ids"] = schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "UUIDs of light services owned by the device."}
	}
	resp.Schema = schema.Schema{Description: "Look up a Hue " + d.kind + " by UUID. Missing resources are errors.", Attributes: attrs}
}
func (d *lookupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	d.client, ok = req.ProviderData.(*hue.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Hue client.")
	}
}
func (d *lookupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !validUUID(id.ValueString()) {
		resp.Diagnostics.AddError("Invalid resource ID", "id must be a UUID.")
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Missing client", "Provider is not configured.")
		return
	}
	set := func(key string, value any) {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
	if d.kind == "light" {
		light, err := hue.GetOne[hue.Light](ctx, d.client, "light", id.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Read light failed", err.Error())
			return
		}
		set("id", id)
		set("name", light.Metadata.Name)
		set("device_id", light.Owner.RID)
		set("supports_color", light.Color != nil)
		set("supports_color_temperature", light.ColorTemperature != nil)
		gamut := types.StringNull()
		if light.Color != nil {
			v := light.Color.GamutType
			if v == "" {
				v = "other"
			}
			gamut = types.StringValue(v)
		}
		set("gamut_type", gamut)
		min, max := types.Int64Null(), types.Int64Null()
		if light.ColorTemperature != nil {
			min = types.Int64Value(light.ColorTemperature.MirekSchema.Min)
			max = types.Int64Value(light.ColorTemperature.MirekSchema.Max)
		}
		set("mirek_min", min)
		set("mirek_max", max)
	} else {
		device, err := hue.GetOne[hue.Device](ctx, d.client, "device", id.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Read device failed", err.Error())
			return
		}
		ids := []string{}
		for _, service := range device.Services {
			if service.RType == "light" {
				ids = append(ids, service.RID)
			}
		}
		set("id", id)
		set("name", device.Metadata.Name)
		set("model_id", device.ProductData.ModelID)
		set("light_ids", ids)
	}
}
