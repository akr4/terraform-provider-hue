package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccMirekFromKelvinFunction(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	output := func(kelvin int) string {
		return accProvider + fmt.Sprintf("output \"mirek\" {\n value = provider::hue::mirek_from_kelvin(%d)\n}\n", kelvin)
	}
	steps := []resource.TestStep{}
	for _, c := range []struct {
		kelvin int
		mirek  string
	}{{2700, "370"}, {6500, "154"}, {1000, "1000"}, {20000, "50"}} {
		steps = append(steps, resource.TestStep{Config: output(c.kelvin), Check: resource.TestCheckOutput("mirek", c.mirek)})
	}
	for _, kelvin := range []int{999, 20203, 0, -2700} {
		steps = append(steps, resource.TestStep{Config: output(kelvin), ExpectError: regexp.MustCompile("kelvin must convert")})
	}
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), Steps: steps})
}

func TestAccSceneMirekFromKelvin(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	config := sceneConfig("on = true\nbrightness = 40\nmirek = provider::hue::mirek_from_kelvin(2700)", "Evening", "room")
	resource.Test(t, resource.TestCase{TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}, ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".mirek", "370"), resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".kelvin", "2703"))},
		{Config: config, PlanOnly: true},
	}})
}
