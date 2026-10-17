package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Values that the bridge stores differently from the configuration apply
// without an inconsistent result and plan no changes (#25, #28).
func TestAccSceneStoredValues(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	action := "actions." + fakebridge.LightID
	temperature := sceneConfig("on = true\nbrightness = 33.33333333333333\nkelvin = 1800", "Candle", "room")
	color := sceneConfig("on = true\nbrightness = 49.80392156862745\ncolor_xy = { x = 0.72345, y = 0.26 }", "Red", "room")
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: temperature, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, action+".kelvin", "1800"),
			resource.TestCheckResourceAttr(addr, action+".mirek", "556"),
			resource.TestCheckResourceAttr(addr, action+".brightness", "33.33333333333333"),
		)},
		{Config: temperature, PlanOnly: true},
		{Config: color, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, action+".color_xy.x", "0.72345"),
			resource.TestCheckResourceAttr(addr, action+".brightness", "49.80392156862745"),
		)},
		{Config: color, PlanOnly: true},
	}})
}

func TestAccScenePaletteStoredBrightness(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	palette := `jsonencode({ color = [{ color = { xy = { x = 0.72345, y = 0.26 } }, dimming = { brightness = 49.80392156862745 } }], dimming = [{ brightness = 33.33333333333333 }] })`
	config := strings.Replace(sceneConfig("on = true", "Palette", "room"), "actions = {", "palette = "+palette+"\n actions = {", 1)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config, Check: func(s *terraform.State) error {
			id := s.RootModule().Resources["hue_scene.test"].Primary.ID
			scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
			if err != nil {
				return err
			}
			if got := string(scene.Palette); !strings.Contains(got, `"brightness":49.8`) || !strings.Contains(got, `"brightness":33.33}`) || !strings.Contains(got, `0.72345`) {
				return fmt.Errorf("palette sent as %s", got)
			}
			return nil
		}},
		{Config: config, PlanOnly: true},
	}})
}
