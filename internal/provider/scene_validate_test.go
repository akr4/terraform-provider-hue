package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// An error in one action does not hide the errors in the others.
func TestValidateConfigReportsEveryAction(t *testing.T) {
	ctx := context.Background()
	r := &sceneResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	bright := emptyAction()
	bright.Brightness = types.Float64Value(101)
	kelvin := emptyAction()
	kelvin.Kelvin = types.Int64Value(2700)
	actions, d := types.MapValueFrom(ctx, actionType, map[string]actionModel{fakebridge.LightID: bright, fakebridge.WhiteLightID: kelvin, "not-a-uuid": emptyAction()})
	if d.HasError() {
		t.Fatal(d)
	}
	m := sceneModel{ID: types.StringUnknown(), Name: types.StringValue("Scene"), Group: types.StringValue(fakebridge.DeviceID), Actions: actions, Speed: types.Float64Null(), AutoDynamic: types.BoolNull(), Palette: types.StringNull()}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	for range 20 {
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}}, &resp)
		actions := path.Root("actions")
		for _, want := range []path.Path{actions.AtMapKey(fakebridge.LightID), actions.AtMapKey(fakebridge.WhiteLightID).AtName("kelvin"), actions.AtMapKey("not-a-uuid")} {
			found := false
			for _, e := range resp.Diagnostics.Errors() {
				if p, ok := e.(interface{ Path() path.Path }); ok && p.Path().Equal(want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no error at %s: %v", want, resp.Diagnostics)
			}
		}
	}
}

// An action that is unknown until apply is checked once apply knows it, before
// the scene is sent: Terraform validates the configuration again then.
func TestAccSceneKelvinUnknownUntilApply(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	config := sceneConfig("on = true", "Unknown", "room")
	config = regexp.MustCompile(`"`+fakebridge.LightID+`" = \{ on = true \}`).ReplaceAllString(config, `"`+fakebridge.LightID+`" = hue_room.test.id != "" ? { kelvin = 2700 } : { kelvin = 2701 }`)
	tfresource.Test(t, tfresource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []tfresource.TestStep{
		{Config: config, ExpectError: regexp.MustCompile(`kelvin was removed in 0.2.0`)},
	}})
	for _, r := range b.Requests() {
		if r.Method != "GET" && strings.Contains(r.Path, "/scene") {
			t.Fatalf("scene written: %s %s", r.Method, r.Path)
		}
	}
}
