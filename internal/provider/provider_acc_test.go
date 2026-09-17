package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const accProvider = `provider "hue" {
 host = "fake.local"
 application_key = "test-key"
}
`

func factories(b *fakebridge.Bridge) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"hue": providerserver.NewProtocol6WithError(&hueProvider{version: "test", client: b.Client()})}
}
func destroyed(b *fakebridge.Bridge) func(*terraform.State) error {
	return func(_ *terraform.State) error {
		for _, kind := range []string{"room", "zone", "scene"} {
			if b.Count(kind) != 0 {
				return fmt.Errorf("%s resources remain", kind)
			}
		}
		return nil
	}
}
func TestAccGroups(t *testing.T) {
	for _, kind := range []string{"room", "zone"} {
		t.Run(kind, func(t *testing.T) {
			b := fakebridge.New()
			defer b.Close()
			id := fakebridge.DeviceID
			if kind == "zone" {
				id = fakebridge.LightID
			}
			config := func(name, children string) string {
				return accProvider + fmt.Sprintf(`
resource "hue_%s" "test" {
 name = %q
 children = %s
}
`, kind, name, children)
			}
			addr := "hue_" + kind + ".test"
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
				{Config: config("Study", fmt.Sprintf("[%q]", id)), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "name", "Study"), resource.TestCheckResourceAttr(addr, "archetype", "other"), resource.TestCheckResourceAttr(addr, "children.#", "1"))},
				{ResourceName: addr, ImportState: true, ImportStateVerify: true},
				{Config: config("Changed", "[]"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "name", "Changed"), resource.TestCheckResourceAttr(addr, "children.#", "0"))},
				{Config: config("Changed", "[]"), PlanOnly: true},
			}})
		})
	}
}
func TestAccDataSources(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{{Config: accProvider + fmt.Sprintf(`
data "hue_light" "desk" { id = %q }
data "hue_device" "desk" { id = %q }
data "hue_light" "white" { id = %q }
`, fakebridge.LightID, fakebridge.DeviceID, fakebridge.WhiteLightID), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("data.hue_light.desk", "device_id", fakebridge.DeviceID), resource.TestCheckResourceAttr("data.hue_light.desk", "gamut_type", "C"), resource.TestCheckResourceAttr("data.hue_light.desk", "mirek_min", "200"), resource.TestCheckResourceAttr("data.hue_device.desk", "light_ids.#", "1"), resource.TestCheckResourceAttr("data.hue_light.white", "supports_color", "false"), resource.TestCheckNoResourceAttr("data.hue_light.white", "mirek_min"))}, {Config: accProvider + `data "hue_light" "missing" { id = "99999999-9999-4999-8999-999999999999" }`, ExpectError: regexp.MustCompile("Read light failed")}}})
}
func sceneConfig(action, name, group string) string {
	return accProvider + fmt.Sprintf(`
resource "hue_room" "test" {
 name = "Study"
 children = [%q]
}
resource "hue_zone" "test" {
 name = "Zone"
 children = [%q]
}
resource "hue_scene" "test" {
 name = %q
 group = hue_%s.test.id
 actions = {
  %q = { %s }
 }
 speed = 0.6
}
`, fakebridge.DeviceID, fakebridge.LightID, name, group, fakebridge.LightID, action)
}
func TestAccScene(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_scene.test"
	var sceneID string
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: sceneConfig("on = true\nbrightness = 40\ncolor_xy = { x = 0.6915, y = 0.3083 }", "Evening", "room"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".color_xy.x", "0.6915"), func(s *terraform.State) error { sceneID = s.RootModule().Resources[addr].Primary.ID; return nil })},
		{Config: sceneConfig("on = true\nbrightness = 40\ncolor_xy = { x = 0.6915, y = 0.3083 }", "Evening", "room"), PlanOnly: true},
		{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		{Config: sceneConfig("on = false\nbrightness = 20\nkelvin = 1000", "Warm", "room"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".kelvin", "1000"), resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".mirek", "450"), resource.TestCheckNoResourceAttr(addr, "actions."+fakebridge.LightID+".color_xy"))},
		{Config: sceneConfig("on = false\nbrightness = 20\nkelvin = 1000", "Warm", "room"), PlanOnly: true},
		{Config: sceneConfig("mirek = 200", "Cool", "room"), Check: resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".kelvin", "5000")},
		{PreConfig: func() {
			scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", sceneID)
			if err != nil {
				t.Fatal(err)
			}
			scene.Actions[0].Action.ColorTemperature.Mirek = 300
			b.Put("scene", sceneID, scene)
		}, Config: sceneConfig("mirek = 200", "Cool", "room"), PlanOnly: true, ExpectNonEmptyPlan: true},
		{Config: sceneConfig("color_xy = { x = 0.9, y = 0.1 }", "Zone scene", "zone"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "actions."+fakebridge.LightID+".color_xy.x", "0.9"), func(s *terraform.State) error {
			if s.RootModule().Resources[addr].Primary.ID == sceneID {
				return fmt.Errorf("group change did not replace scene")
			}
			return nil
		})},
		{Config: sceneConfig("color_xy = { x = 0.9, y = 0.1 }", "Zone scene", "zone"), PlanOnly: true},
	}})
}
func TestAccValidation(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{{Config: sceneConfig("mirek = 370\nkelvin = 2700", "Invalid", "room"), ExpectError: regexp.MustCompile("cannot both be configured")}}})
}

func TestAccSceneActionsAndMetadata(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	var id string
	checkImage := func(s *terraform.State) error {
		scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
		if err != nil {
			return err
		}
		if scene.Metadata.Image == nil || scene.Metadata.Image.RID != "66666666-6666-4666-8666-666666666666" {
			return fmt.Errorf("remote image changed")
		}
		if s.RootModule().Resources["hue_scene.test"].Primary.ID != id {
			return fmt.Errorf("scene replaced")
		}
		return nil
	}
	config := func(two bool) string {
		hcl := sceneConfig("on = true\ncolor_xy = { x = 0.6915, y = 0.3083 }", "Original", "room")
		if two {
			hcl = strings.Replace(hcl, "actions = {", "actions = {\n\""+fakebridge.WhiteLightID+"\" = { on = false }", 1)
		}
		return hcl
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config(false), Check: func(s *terraform.State) error { id = s.RootModule().Resources["hue_scene.test"].Primary.ID; return nil }},
		{PreConfig: func() {
			scene, err := hue.GetOne[hue.Scene](context.Background(), b.Client(), "scene", id)
			if err != nil {
				t.Fatal(err)
			}
			scene.Metadata.Image = &hue.Reference{RID: "66666666-6666-4666-8666-666666666666", RType: "public_image"}
			scene.Palette = json.RawMessage(`{"color":[],"dimming":[{"brightness":42}],"color_temperature":[]}`)
			b.Put("scene", id, scene)
		}, ResourceName: "hue_scene.test", ImportState: true, ImportStateCheck: func(states []*terraform.InstanceState) error {
			if len(states) != 1 || states[0].Attributes["image_id"] != "" {
				return fmt.Errorf("image unexpectedly managed on import")
			}
			if !strings.Contains(states[0].Attributes["palette"], "42") {
				return fmt.Errorf("palette missing on import")
			}
			return nil
		}},
		{Config: config(true), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("hue_scene.test", "actions.%", "2"), checkImage)},
		{Config: strings.Replace(config(false), "Original", "Renamed", 1), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("hue_scene.test", "actions.%", "1"), checkImage)},
		{Config: strings.Replace(config(false), "Original", "Renamed", 1), PlanOnly: true},
	}})
	for _, req := range b.Requests() {
		if (req.Method == "PUT" || req.Method == "POST") && strings.Contains(req.Path, "/scene") {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(req.Body, &payload); err != nil {
				t.Fatal(err)
			}
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(payload["metadata"], &metadata); err != nil {
				t.Fatal(err)
			}
			if _, ok := metadata["image"]; ok {
				t.Fatal("resent unchanged scene image")
			}
			if _, ok := payload["palette"]; ok {
				t.Fatal("wrote palette")
			}
			if _, ok := payload["group"]; ok && req.Method == "PUT" {
				t.Fatal("wrote immutable group")
			}
		}
	}
}
