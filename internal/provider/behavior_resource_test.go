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

const behaviorTestID = "44444444-4444-4444-8444-444444444444"
const behaviorScriptID = "55555555-5555-4555-8555-555555555555"

func TestBehaviorJSONEquality(t *testing.T) {
	for _, tt := range []struct {
		a, b  string
		equal bool
	}{
		{`{"a":1,"b":[true,null]}`, `{ "b": [true,null], "a": 1.0 }`, true},
		{`{"a":[1,2]}`, `{"a":[2,1]}`, false},
		{`{"a":false}`, `{}`, false},
		{`{"a":9007199254740993}`, `{"a":9007199254740992}`, false},
		{`{}`, `bad`, false},
	} {
		if sameJSON([]byte(tt.a), []byte(tt.b)) != tt.equal {
			t.Fatalf("%s / %s", tt.a, tt.b)
		}
	}
}
func TestAccBehaviorInstance(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	original := hue.BehaviorInstance{ID: behaviorTestID, Type: "behavior_instance", ScriptID: behaviorScriptID, Enabled: true, Metadata: hue.Metadata{Name: "Switch"}, Status: "running", Configuration: json.RawMessage(`{"device":{"rid":"22222222-2222-4222-8222-222222222222","rtype":"device"},"buttons":{"one":{"scene":"old","hold":"dim_up"}},"future":{"keep":[false,null,1.0]}}`)}
	b.Put("behavior_instance", behaviorTestID, original)
	config := func(scene string, enabled bool) string {
		return accProvider + fmt.Sprintf(`
resource "hue_behavior_instance" "test" {
 name = "Switch"
 enabled = %t
 configuration = jsonencode({
  device = { rid = %q, rtype = "device" }
  buttons = { one = { scene = %q, hold = "dim_up" } }
  future = { keep = [false, null, 1] }
 })
}
`, enabled, fakebridge.DeviceID, scene)
	}
	addr := "hue_behavior_instance.test"
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: func(_ *terraform.State) error {
		if b.Count("behavior_instance") != 1 {
			return fmt.Errorf("forgotten behavior deleted")
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: config("old", true) + fmt.Sprintf("\nimport {\n to = hue_behavior_instance.test\n id = %q\n}\n", behaviorTestID), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "id", behaviorTestID), resource.TestCheckResourceAttr(addr, "script_id", behaviorScriptID))},
		{Config: config("old", true), PlanOnly: true},
		{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		{Config: config("new", false), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "enabled", "false"), resource.TestCheckResourceAttr(addr, "status", "disabled"))},
		{Config: config("new", false), PlanOnly: true},
		{PreConfig: func() {
			v, err := hue.GetOne[hue.BehaviorInstance](context.Background(), b.Client(), "behavior_instance", behaviorTestID)
			if err != nil {
				t.Fatal(err)
			}
			v.Configuration = json.RawMessage(strings.Replace(string(v.Configuration), `"new"`, `"app change"`, 1))
			b.Put("behavior_instance", behaviorTestID, v)
		}, Config: config("new", false), PlanOnly: true, ExpectNonEmptyPlan: true},
		{Config: config("new", false)},
		{Config: accProvider + `removed {
 from = hue_behavior_instance.test
 lifecycle { destroy = false }
}`},
	}})
	writes := 0
	for _, req := range b.Requests() {
		if req.Method == "POST" || req.Method == "DELETE" {
			t.Fatalf("unexpected mutation: %s %s", req.Method, req.Path)
		}
		if req.Method != "PUT" {
			continue
		}
		writes++
		var patch map[string]json.RawMessage
		_ = json.Unmarshal(req.Body, &patch)
		if len(patch) != 3 || patch["configuration"] == nil || patch["enabled"] == nil || patch["metadata"] == nil {
			t.Fatalf("unexpected update fields: %s", req.Body)
		}
		if !strings.Contains(string(patch["configuration"]), `"future"`) || !strings.Contains(string(patch["configuration"]), `"dim_up"`) {
			t.Fatal("lost unrelated settings")
		}
	}
	if writes != 2 {
		t.Fatalf("expected two deliberate updates, got %d", writes)
	}
}
func TestAccBehaviorValidation(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	config := func(value string) string {
		return accProvider + fmt.Sprintf(`resource "hue_behavior_instance" "test" {
 name = "Switch"
 enabled = false
 configuration = %q
}`, value)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{
		{Config: config(`[]`), ExpectError: regexp.MustCompile("must be a JSON object")},
		{Config: config(`null`), ExpectError: regexp.MustCompile("must be a JSON object")},
		{Config: config(`{}`), ExpectError: regexp.MustCompile("Script ID required for creation")},
	}})
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("invalid config wrote to bridge")
		}
	}
}

func TestAccBehaviorCreate(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	addr := "hue_behavior_instance.test"
	config := func(script, name string) string {
		return accProvider + fmt.Sprintf(`
resource "hue_behavior_instance" "test" {
 name = %q
 enabled = true
 script_id = %q
 configuration = jsonencode({ device = { rid = %q, rtype = "device" }, buttons = {} })
}`, name, script, fakebridge.DeviceID)
	}
	var firstID string
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: func(_ *terraform.State) error {
		if b.Count("behavior_instance") != 0 {
			return fmt.Errorf("behavior remains")
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: config(behaviorScriptID, "Switch"), Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(addr, "script_id", behaviorScriptID), resource.TestCheckResourceAttr(addr, "status", "running"), func(s *terraform.State) error { firstID = s.RootModule().Resources[addr].Primary.ID; return nil })},
		{Config: config(behaviorScriptID, "Switch"), PlanOnly: true},
		{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		{Config: config(behaviorScriptID, "Renamed")},
		{Config: config("66666666-6666-4666-8666-666666666666", "Renamed"), Check: func(s *terraform.State) error {
			if s.RootModule().Resources[addr].Primary.ID == firstID {
				return fmt.Errorf("script change did not replace behavior")
			}
			return nil
		}},
		{Config: config("66666666-6666-4666-8666-666666666666", "Renamed"), PlanOnly: true},
	}})
	creates, deletes := 0, 0
	for _, r := range b.Requests() {
		if r.Method == "POST" {
			creates++
			var body map[string]json.RawMessage
			_ = json.Unmarshal(r.Body, &body)
			if len(body) != 5 || body["script_id"] == nil || body["type"] == nil {
				t.Fatalf("invalid creation payload %s", r.Body)
			}
		}
		if r.Method == "DELETE" {
			deletes++
		}
		if r.Method == "PUT" {
			var body map[string]json.RawMessage
			_ = json.Unmarshal(r.Body, &body)
			if body["script_id"] != nil {
				t.Fatal("updated immutable script")
			}
		}
	}
	if creates != 2 || deletes != 2 {
		t.Fatalf("creates=%d deletes=%d", creates, deletes)
	}
}
func TestAccBehaviorDuplicate(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	b.Put("behavior_instance", behaviorTestID, hue.BehaviorInstance{ID: behaviorTestID, Type: "behavior_instance", ScriptID: behaviorScriptID, Configuration: json.RawMessage(`{"device":{"rid":"22222222-2222-4222-8222-222222222222","rtype":"device"}}`)})
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), Steps: []resource.TestStep{{Config: accProvider + fmt.Sprintf(`
resource "hue_behavior_instance" "test" {
 name = "Duplicate"
 enabled = true
 script_id = %q
 configuration = jsonencode({device = {rid = %q, rtype = "device"}})
}`, behaviorScriptID, fakebridge.DeviceID), ExpectError: regexp.MustCompile("already has behavior")}}})
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("wrote duplicate assignment")
		}
	}
}
