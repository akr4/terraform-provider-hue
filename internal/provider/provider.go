package provider

import (
	"context"
	"os"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type hueProvider struct {
	version string
	client  *hue.Client
}
type providerModel struct {
	Host types.String `tfsdk:"host"`
	Key  types.String `tfsdk:"application_key"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &hueProvider{version: version} }
}
func (p *hueProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hue"
	resp.Version = p.version
}
func (p *hueProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manage device settings, rooms, zones, scenes, smart scenes and behavior instances on a Philips Hue bridge using API v2.", Attributes: map[string]schema.Attribute{
		"host":            schema.StringAttribute{Optional: true, Description: "Bridge IP address or hostname, without scheme or port. Falls back to HUE_BRIDGE_HOST."},
		"application_key": schema.StringAttribute{Optional: true, Sensitive: true, Description: "Hue application key. Falls back to HUE_BRIDGE_APPLICATION_KEY."},
	}}
}
func (p *hueProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Host.IsUnknown() || config.Key.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration", "host and application_key must be known before configuring the provider.")
		return
	}
	host, key := os.Getenv("HUE_BRIDGE_HOST"), os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if !config.Host.IsNull() {
		host = config.Host.ValueString()
	}
	if !config.Key.IsNull() {
		key = config.Key.ValueString()
	}
	if host == "" || key == "" {
		resp.Diagnostics.AddError("Missing provider configuration", "Set host and application_key or their HUE_BRIDGE_ environment variables.")
		return
	}
	var err error
	client := p.client
	if client == nil {
		client, err = hue.NewClient(host, key)
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid bridge configuration", err.Error())
		return
	}
	client.ResetLightCapabilities()
	resp.DataSourceData = client
	resp.ResourceData = client
}
func (p *hueProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return &deviceResource{} }, func() resource.Resource { return &groupResource{kind: "room", childKind: "device"} }, func() resource.Resource { return &groupResource{kind: "zone", childKind: "light"} }, func() resource.Resource { return &sceneResource{} }, func() resource.Resource { return &behaviorResource{} }, func() resource.Resource { return &smartSceneResource{} }}
}
func (p *hueProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{func() datasource.DataSource { return &lookupDataSource{kind: "light"} }, func() datasource.DataSource { return &lookupDataSource{kind: "device"} }}
}
