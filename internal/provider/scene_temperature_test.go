package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// The state holds the color temperature in the form that the configuration
// uses. Moving between kelvin and mirek plans one out and the other in, and
// applying it sends the same mirek again.
func TestAccSceneTemperatureForm(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	action := tfjsonpath.New("actions").AtMapKey(fakebridge.LightID)
	attr := "actions." + fakebridge.LightID + "."
	mirek := sceneConfig("mirek = 370", "Warm", "room")
	kelvin := sceneConfig("kelvin = 2700", "Warm", "room")
	// only checks that the state holds name and not other, and that the bridge
	// still stores the same mirek.
	only := func(name, value, other string) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, attr+name, value),
			resource.TestCheckNoResourceAttr(addr, attr+other),
			func(s *terraform.State) error {
				scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", s.RootModule().Resources[addr].Primary.ID)
				if err != nil {
					return err
				}
				if temperature := scene.Actions[0].Action.ColorTemperature; temperature == nil || temperature.Mirek != 370 {
					return fmt.Errorf("bridge stores %+v", temperature)
				}
				return nil
			},
		)
	}
	swapped := func(in, out string) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			plancheck.ExpectKnownValue(addr, action.AtMapKey(in), knownvalue.NotNull()),
			plancheck.ExpectKnownValue(addr, action.AtMapKey(out), knownvalue.Null()),
		}}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		// A new scene with mirek plans no kelvin.
		{Config: mirek, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectKnownValue(addr, action.AtMapKey("kelvin"), knownvalue.Null()),
		}}, Check: only("mirek", "370", "kelvin")},
		{Config: kelvin, ConfigPlanChecks: swapped("kelvin", "mirek"), Check: only("kelvin", "2700", "mirek")},
		{Config: kelvin, PlanOnly: true},
		{Config: mirek, ConfigPlanChecks: swapped("mirek", "kelvin"), Check: only("mirek", "370", "kelvin")},
		{Config: mirek, PlanOnly: true},
	}})
}
