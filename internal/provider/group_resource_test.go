package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const appSceneID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
const appSmartSceneID = "abababab-abab-4bab-8bab-abababababab"

func groupConfig(kind, child, extra string) string {
	return accProvider + fmt.Sprintf(`
resource "hue_%s" "test" {
 name = "Group"
 children = [%q]
 %s
}
`, kind, child, extra)
}
func captureID(addr string, id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[addr].Primary.ID
		return nil
	}
}

// putAppScenes adds scenes to a group as if they had been made in the Hue app.
func putAppScenes(b *fakebridge.Bridge, group hue.Reference, smart bool) {
	b.Put("scene", appSceneID, hue.Scene{ID: appSceneID, Type: "scene", Metadata: hue.Metadata{Name: "App scene"}, Group: group})
	if smart {
		b.Put("smart_scene", appSmartSceneID, hue.SmartScene{ID: appSmartSceneID, Type: "smart_scene", Metadata: hue.Metadata{Name: "Natural light"}, Group: group, State: "inactive"})
	}
}

func TestAccGroupForceDestroy(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_zone.test"
	var zoneID string
	var before int
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: groupConfig("zone", fakebridge.LightID, ""), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "force_destroy", "false"), captureID(addr, &zoneID))},
		{PreConfig: func() { putAppScenes(b, hue.Reference{RID: zoneID, RType: "zone"}, true) }, Config: accProvider, ExpectError: regexp.MustCompile(`(?s)Zone still contains scenes.*"App scene".*"Natural light" \(smart scene\).*force_destroy = true`)},
		{PreConfig: func() {
			if _, err := hue.GetOne[hue.Group](t.Context(), b.Client(), "zone", zoneID); err != nil {
				t.Fatalf("refused deletion removed the zone: %v", err)
			}
			if _, err := b.ReadScene(t.Context(), appSceneID); err != nil {
				t.Fatalf("refused deletion removed the scene: %v", err)
			}
			before = len(b.Requests())
		}, Config: groupConfig("zone", fakebridge.LightID, "force_destroy = true"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "force_destroy", "true"), func(*terraform.State) error {
			for _, req := range b.Requests()[before:] {
				if req.Method != "GET" {
					return fmt.Errorf("force_destroy change wrote to the bridge: %s %s", req.Method, req.Path)
				}
			}
			return nil
		})},
		{Config: groupConfig("zone", fakebridge.LightID, "force_destroy = true"), PlanOnly: true},
		{Config: accProvider, Check: func(*terraform.State) error {
			if b.Count("scene") != 0 || b.Count("smart_scene") != 0 {
				return fmt.Errorf("deleting the zone did not delete its scenes")
			}
			return nil
		}},
	}})
}

func TestAccRoomRefusesDeletionWithScenes(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	var roomID string
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: groupConfig("room", fakebridge.DeviceID, ""), Check: captureID("hue_room.test", &roomID)},
		{PreConfig: func() { putAppScenes(b, hue.Reference{RID: roomID, RType: "room"}, false) }, Config: accProvider, ExpectError: regexp.MustCompile(`(?s)Room still contains scenes.*"App scene"`)},
		{PreConfig: func() { b.Remove("scene", appSceneID) }, Config: accProvider},
	}})
}

// Terraform deletes scenes that reference the zone before the zone itself.
func TestAccZoneDeletesManagedScenesFirst(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: groupConfig("zone", fakebridge.LightID, "") + fmt.Sprintf(`
resource "hue_scene" "test" {
 name = "Managed"
 group = hue_zone.test.id
 actions = { %q = { on = true } }
}
`, fakebridge.LightID)},
		{Config: accProvider},
	}})
}
