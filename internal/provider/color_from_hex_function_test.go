package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccColorFromHexFunction(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	output := func(hex string) string {
		return accProvider + fmt.Sprintf("output \"color\" {\n value = provider::hue::color_from_hex(%q)\n}\n", hex)
	}
	color := func(x, y, brightness float64) []statecheck.StateCheck {
		return []statecheck.StateCheck{statecheck.ExpectKnownOutputValue("color", knownvalue.ObjectExact(map[string]knownvalue.Check{
			"color_xy":   knownvalue.ObjectExact(map[string]knownvalue.Check{"x": knownvalue.Float64Exact(x), "y": knownvalue.Float64Exact(y)}),
			"brightness": knownvalue.Float64Exact(brightness),
		}))}
	}
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: output("#ff8800"), ConfigStateChecks: color(0.601, 0.3837, 100)},
		{Config: output("#7f4400"), ConfigStateChecks: color(0.5926, 0.3908, 49.8)},
		{Config: output("ff8800"), ExpectError: regexp.MustCompile("#rrggbb or #rgb")},
		{Config: output("#000000"), ExpectError: regexp.MustCompile("has no color")},
	}})
}

func TestAccSceneColorFromHex(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	action := "actions." + fakebridge.LightID
	config := func(settings string) string {
		return accProvider + fmt.Sprintf(`
resource "hue_room" "test" {
 name = "Study"
 children = [%q]
}
resource "hue_scene" "test" {
 name = "Sunset"
 group = hue_room.test.id
 actions = {
  %q = merge(provider::hue::color_from_hex("#7f4400"), %s)
 }
}
`, fakebridge.DeviceID, fakebridge.LightID, settings)
	}
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config("{ on = true }"), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, action+".on", "true"),
			resource.TestCheckResourceAttr(addr, action+".color_xy.x", "0.5926"),
			resource.TestCheckResourceAttr(addr, action+".color_xy.y", "0.3908"),
			resource.TestCheckResourceAttr(addr, action+".brightness", "49.8"),
		)},
		{Config: config("{ on = true }"), PlanOnly: true},
		// Settings after the function win.
		{Config: config("{ on = true, brightness = 40 }"), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, action+".color_xy.x", "0.5926"),
			resource.TestCheckResourceAttr(addr, action+".brightness", "40"),
		)},
		{Config: config("{ on = true, brightness = 40 }"), PlanOnly: true},
	}})
}
