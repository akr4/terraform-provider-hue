package provider

import (
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
