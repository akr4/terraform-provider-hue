package provider

import (
	"context"
	"encoding/json"
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

// An action that stays unknown until apply must not hide the values that can
// already be planned: speed and palette, and the other lights' actions.
func TestAccScenePlanWithUnknownAction(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	var id string
	config := func(extra, first string) string {
		return accProvider + extra + fmt.Sprintf(`
resource "hue_zone" "test" {
 name = "Zone"
 children = [%[1]q, %[2]q]
}
resource "hue_scene" "test" {
 name = "Scene"
 group = hue_zone.test.id
 actions = {
  %[1]q = { on = true, mirek = 370 }
  %[2]q = %[3]s
 }
}
`, fakebridge.LightID, fakebridge.WhiteLightID, first)
	}
	// terraform_data is new in the second step, so its output is unknown until apply.
	unknownAction := config(`
resource "terraform_data" "action" {
 input = { on = false, brightness = 10 }
}
`, "terraform_data.action.output")
	color := tfjsonpath.New("actions").AtMapKey(fakebridge.LightID)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config("", "{ on = true, brightness = 40 }"), Check: func(s *terraform.State) error { id = s.RootModule().Resources[addr].Primary.ID; return nil }},
		{PreConfig: func() {
			// Values set outside Terraform, as the Hue app does.
			scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
			if err != nil {
				t.Fatal(err)
			}
			speed := 0.4
			scene.Speed = &speed
			scene.Palette = json.RawMessage(`{"color":[],"dimming":[{"brightness":42}],"color_temperature":[]}`)
			for i := range scene.Actions {
				if scene.Actions[i].Target.RID == fakebridge.LightID {
					scene.Actions[i].Action.Dynamics = json.RawMessage(`{"duration":800}`)
				}
			}
			b.Put("scene", id, scene)
		}, Config: unknownAction, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			plancheck.ExpectUnknownValue(addr, tfjsonpath.New("actions").AtMapKey(fakebridge.WhiteLightID).AtMapKey("on")),
			plancheck.ExpectKnownValue(addr, tfjsonpath.New("speed"), knownvalue.Float64Exact(0.4)),
			plancheck.ExpectKnownValue(addr, tfjsonpath.New("palette").AtMapKey("dimming").AtSliceIndex(0).AtMapKey("brightness"), knownvalue.Float64Exact(42)),
			plancheck.ExpectKnownValue(addr, color.AtMapKey("mirek"), knownvalue.Int64Exact(370)),
			plancheck.ExpectKnownValue(addr, color.AtMapKey("dynamics").AtMapKey("duration"), knownvalue.Int64Exact(800)),
			plancheck.ExpectKnownValue(addr, color.AtMapKey("gradient"), knownvalue.Null()),
		}}, Check: func(s *terraform.State) error {
			scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
			if err != nil {
				return err
			}
			if s.RootModule().Resources[addr].Primary.ID != id || scene.Speed == nil || *scene.Speed != 0.4 || len(scene.Palette) == 0 {
				return fmt.Errorf("scene replaced or values set outside Terraform lost: %+v", scene)
			}
			return nil
		}},
		{Config: unknownAction, PlanOnly: true},
	}})
}
