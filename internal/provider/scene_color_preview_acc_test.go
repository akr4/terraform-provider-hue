package provider

import (
	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"testing"
)

func TestAccSceneXYHexPreview(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	steps := []resource.TestStep{}
	for _, xy := range []struct {
		hcl   string
		point hue.XY
	}{
		{"color_xy = { x = 0.4964, y = 0.4542 }", hue.XY{X: .4964, Y: .4542}},
		{"color_xy = { x = 0.1554, y = 0.0996 }", hue.XY{X: .1554, Y: .0996}},
		{"color_xy = { x = 0.8, y = 0.1 }", hue.XY{X: .8, Y: .1}},
	} {
		hex := colors.XYToHex(xy.point)
		cfg := sceneConfig("on = true\nbrightness = 40\n"+xy.hcl, "Preview", "room")
		steps = append(steps, resource.TestStep{Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectKnownValue("hue_scene.test", tfjsonpath.New("actions").AtMapKey(fakebridge.LightID).AtMapKey("color_hex"), knownvalue.StringExact(hex)),
			}},
			Check: resource.TestCheckResourceAttr("hue_scene.test", "actions."+fakebridge.LightID+".color_hex", hex),
		}, resource.TestStep{Config: cfg, PlanOnly: true})
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: steps})
}
