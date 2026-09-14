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
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccDeviceSettings(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	id := fakebridge.DeviceID
	other := "99999999-9999-4999-8999-999999999999"
	b.Put("device", id, hue.Device{ID: id, Type: "device", Metadata: hue.Metadata{Name: "Original", Archetype: "sultan_bulb"}, Services: []hue.Reference{{RID: fakebridge.LightID, RType: "light"}}})
	b.Put("device", other, hue.Device{ID: other, Type: "device", Metadata: hue.Metadata{Name: "Second", Archetype: "sultan_bulb"}})
	cfg := func(id, attrs string) string {
		return accProvider + fmt.Sprintf("\nresource \"hue_device\" \"test\" {\n device_id = %q\n %s\n}\n", id, attrs)
	}
	addr := "hue_device.test"
	check := func(id, name, icon string) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "id", id), resource.TestCheckResourceAttr(addr, "device_id", id),
			resource.TestCheckResourceAttr(addr, "name", name), resource.TestCheckResourceAttr(addr, "archetype", icon))
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: func(_ *terraform.State) error {
		if b.Count("device") < 2 {
			return fmt.Errorf("destroy unpaired a device")
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: cfg(id, `name = ""`), ExpectError: regexp.MustCompile("Invalid name")},
		{Config: cfg("invalid", ""), ExpectError: regexp.MustCompile("Invalid device ID")},
		{Config: cfg(id, `name = "寝室の照明"`), Check: check(id, "寝室の照明", "sultan_bulb")},
		{Config: cfg(id, `name = "寝室の照明"`), PlanOnly: true},
		{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		{Config: cfg(id, `archetype = "table_shade"`), Check: check(id, "寝室の照明", "table_shade")},
		{PreConfig: func() {
			d, err := hue.GetOne[hue.Device](context.Background(), b.Client(), "device", id)
			if err != nil {
				t.Fatal(err)
			}
			d.Metadata.Name = "App name"
			b.Put("device", id, d)
		}, Config: cfg(id, `archetype = "table_shade"`), Check: check(id, "App name", "table_shade")},
		{Config: cfg(id, `name = "Managed name"`), Check: check(id, "Managed name", "table_shade")},
		{PreConfig: func() {
			d, err := hue.GetOne[hue.Device](context.Background(), b.Client(), "device", id)
			if err != nil {
				t.Fatal(err)
			}
			d.Metadata.Name = "External drift"
			b.Put("device", id, d)
		}, Config: cfg(id, `name = "Managed name"`), Check: check(id, "Managed name", "table_shade")},
		{Config: cfg(other, `name = "New binding"`), Check: check(other, "New binding", "sultan_bulb")},
	}})
	for _, r := range b.Requests() {
		if !strings.Contains(r.Path, "/device") {
			continue
		}
		if r.Method == "POST" || r.Method == "DELETE" {
			t.Fatalf("hardware lifecycle request: %+v", r)
		}
		if r.Method == "PUT" {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(r.Body, &m); err != nil {
				t.Fatal(err)
			}
			if len(m) != 1 || m["metadata"] == nil {
				t.Fatal("sent non-metadata device settings")
			}
			var fields map[string]string
			_ = json.Unmarshal(m["metadata"], &fields)
			// Each test config sets only one property: the omitted one must never be resent.
			if len(fields) != 1 {
				t.Fatal("resent omitted field", fields)
			}
		}
	}
	d, err := hue.GetOne[hue.Device](context.Background(), b.Client(), "device", id)
	if err != nil || d.Metadata.Name != "Managed name" || len(d.Services) != 1 {
		t.Fatal("old device modified on replacement", d, err)
	}
}
func TestAccDeviceMissing(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: accProvider + `resource "hue_device" "missing" {device_id = "99999999-9999-4999-8999-999999999999"}`, ExpectError: regexp.MustCompile("Read existing device failed")},
	}})
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("wrote while adopting missing device")
		}
	}
}

func TestAccDeviceAdoptUnconfigured(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	b.Put("device", fakebridge.DeviceID, hue.Device{ID: fakebridge.DeviceID, Type: "device", Metadata: hue.Metadata{Name: "Existing"}})
	cfg := accProvider + fmt.Sprintf(`resource "hue_device" "test" { device_id = %q }`, fakebridge.DeviceID)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("hue_device.test", "name", "Existing"),
			resource.TestCheckNoResourceAttr("hue_device.test", "archetype"))},
		{Config: cfg, PlanOnly: true},
		{ResourceName: "hue_device.test", ImportState: true, ImportStateVerify: true},
	}})
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatalf("adoption without settings wrote to the Bridge: %+v", r)
		}
	}
}
