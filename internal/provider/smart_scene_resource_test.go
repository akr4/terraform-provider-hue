package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const smartGroupID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const smartDaySceneID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const smartNightSceneID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

func seedSmartSceneGroup(b *fakebridge.Bridge) {
	b.Put("room", smartGroupID, hue.Group{ID: smartGroupID, Type: "room", Metadata: hue.Metadata{Name: "Room"}})
	for _, id := range []string{smartDaySceneID, smartNightSceneID} {
		b.Put("scene", id, hue.Scene{ID: id, Type: "scene", Metadata: hue.Metadata{Name: "Scene"}, Group: hue.Reference{RID: smartGroupID, RType: "room"}})
	}
}
func smartConfig(name, start, target string) string {
	return accProvider + fmt.Sprintf(`
resource "hue_smart_scene" "test" {
 name = %q
 group = %q
 transition_duration = 45000
 week_timeslots = [{
  recurrence = ["sunday", "monday"]
  timeslots = [
   { start_time = "07:00:00", scene = %q },
   { start_time = %q, scene = %q },
  ]
 }]
}
`, name, smartGroupID, smartDaySceneID, start, target)
}
func TestAccSmartScene(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	seedSmartSceneGroup(b)
	var id string
	addr := "hue_smart_scene.test"
	check := func(s *terraform.State) error { id = s.RootModule().Resources[addr].Primary.ID; return nil }
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: func(*terraform.State) error {
		if b.Count("smart_scene") != 0 {
			return fmt.Errorf("smart scene remains")
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: smartConfig("Natural", "sunset", smartDaySceneID), Check: resource.ComposeAggregateTestCheckFunc(check, resource.TestCheckResourceAttr(addr, "state", "inactive"), resource.TestCheckResourceAttr(addr, "week_timeslots.0.timeslots.1.start_time", "sunset"))},
		{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		{Config: smartConfig("Natural night", "00:00:00", smartNightSceneID), Check: resource.TestCheckResourceAttr(addr, "week_timeslots.0.timeslots.1.scene", smartNightSceneID)},
		{Config: smartConfig("Natural night", "00:00:00", smartNightSceneID), PlanOnly: true},
		{PreConfig: func() {
			r, err := hue.GetOne[hue.SmartScene](context.Background(), b.Client(), "smart_scene", id)
			if err != nil {
				t.Fatal(err)
			}
			r.Metadata.Name = "App edit"
			r.State = "active"
			r.Metadata.Image = &hue.Reference{RID: fakebridge.DeviceID, RType: "public_image"}
			b.Put("smart_scene", id, r)
		}, Config: smartConfig("Natural night", "00:00:00", smartNightSceneID), PlanOnly: true, ExpectNonEmptyPlan: true},
		{Config: smartConfig("Natural night", "00:00:00", smartNightSceneID), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "state", "active"), resource.TestCheckResourceAttr(addr, "image_id", fakebridge.DeviceID))},
	}})
	for _, r := range b.Requests() {
		if r.Method != "POST" && r.Method != "PUT" {
			continue
		}
		if r.Path != "/clip/v2/resource/smart_scene" && r.Path != "/clip/v2/resource/smart_scene/"+id {
			continue
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"recall", "state", "active_timeslot"} {
			if body[key] != nil {
				t.Fatalf("runtime field sent: %s", r.Body)
			}
		}
		if r.Method == "PUT" && body["group"] != nil {
			t.Fatal("group sent on update")
		}
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(body["metadata"], &metadata)
		if metadata["image"] != nil {
			t.Fatal("image sent")
		}
	}
}
func TestAccSmartSceneInvalidSchedule(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	seedSmartSceneGroup(b)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: smartConfig("Invalid", "24:00:00", smartDaySceneID), ExpectError: regexp.MustCompile("HH:MM:SS")},
	}})
	for _, r := range b.Requests() {
		if r.Method == "POST" || r.Method == "PUT" {
			t.Fatal("invalid schedule written")
		}
	}
}
