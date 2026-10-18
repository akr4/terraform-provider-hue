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
	temperature := sceneConfig("on = true\nbrightness = 33.33333333333333\nmirek = 556", "Candle", "room")
	color := sceneConfig("on = true\nbrightness = 49.80392156862745\ncolor_xy = { x = 0.72345, y = 0.26 }", "Red", "room")
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: temperature, Check: resource.ComposeAggregateTestCheckFunc(
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

func TestAccScenePaletteStoredValues(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	config := paletteSceneConfig(`palette = {
  color   = [{ color_xy = { x = 0.72345, y = 0.26 }, brightness = 49.80392156862745 }]
  dimming = [{ brightness = 33.33333333333333 }]
 }`)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeAggregateTestCheckFunc(
			func(s *terraform.State) error {
				id := s.RootModule().Resources[addr].Primary.ID
				scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
				if err != nil {
					return err
				}
				if got := string(scene.Palette); !strings.Contains(got, `"x":0.7235`) || !strings.Contains(got, `"brightness":49.8}`) || !strings.Contains(got, `"brightness":33.33}`) {
					return fmt.Errorf("palette sent as %s", got)
				}
				return nil
			},
			resource.TestCheckResourceAttr(addr, "palette.color.0.color_xy.x", "0.72345"),
			resource.TestCheckResourceAttr(addr, "palette.color.0.brightness", "49.80392156862745"),
		)},
		{Config: config, PlanOnly: true},
	}})
}
