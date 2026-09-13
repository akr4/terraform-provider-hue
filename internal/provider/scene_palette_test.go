package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestPaletteReconciliation(t *testing.T) {
	a := `{"color":[{"color":{"xy":{"x":0.16,"y":0.1}},"dimming":{"brightness":10}}]}`
	b := `{"color":[{"color":{"xy":{"x":0.16,"y":0.0999999}},"dimming":{"brightness":10}}],"effects":[],"dimming":[]}`
	if !palettesEqual([]byte(a), []byte(b)) {
		t.Fatal("bridge normalization differs")
	}
	got, err := reconcilePalette(types.StringValue(a), []byte(b))
	if err != nil || got.ValueString() != a {
		t.Fatal(got, err)
	}
	if palettesEqual([]byte(a), []byte(strings.ReplaceAll(b, "0.16", "0.20"))) {
		t.Fatal("real drift ignored")
	}
	for _, raw := range []string{"[]", "null", "bad"} {
		if palettesEqual([]byte(raw), []byte(raw)) {
			t.Fatal("invalid palette equal")
		}
	}
}
func TestAccScenePalette(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	var id string
	config := func(name, palette string) string {
		c := sceneConfig("on = true", name, "room")
		if palette != "" {
			c = strings.Replace(c, "actions = {", "palette = "+fmt.Sprintf("%q", palette)+"\n actions = {", 1)
		}
		return c
	}
	first := `{"color":[{"color":{"xy":{"x":0.16,"y":0.1}},"dimming":{"brightness":10}}]}`
	second := `{"color":[{"color":{"xy":{"x":0.45,"y":0.25}},"dimming":{"brightness":20}}]}`
	check := func(want string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			id = s.RootModule().Resources["hue_scene.test"].Primary.ID
			got, err := b.ReadScene(context.Background(), id)
			if err != nil {
				return err
			}
			if !palettesEqual(got.Palette, []byte(want)) {
				return fmt.Errorf("palette got %s want %s", got.Palette, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories(b), CheckDestroy: destroyed(b), Steps: []resource.TestStep{
		{Config: config("invalid", `[]`), ExpectError: regexp.MustCompile("Invalid palette")},
		{Config: config("first", first), Check: check(first)},
		{PreConfig: func() {
			s, err := b.ReadScene(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			s.Palette = json.RawMessage(strings.TrimSuffix(strings.ReplaceAll(first, "0.1}", "0.0999999}"), "}") + `,"effects":[]}`)
			b.Put("scene", id, s)
		}, Config: config("first", first), PlanOnly: true},
		{Config: config("second", second), Check: check(second)},
		{ResourceName: "hue_scene.test", ImportState: true, ImportStateCheck: func(states []*terraform.InstanceState) error {
			if len(states) != 1 || !palettesEqual([]byte(states[0].Attributes["palette"]), []byte(second)) {
				return fmt.Errorf("palette lost on import")
			}
			return nil
		}},
		{Config: config("preserved", ""), Check: check(second)},
		{Config: config("cleared", `{"color":[],"dimming":[],"color_temperature":[]}`), Check: check(`{}`)},
		{Config: config("cleared", `{"color":[],"dimming":[],"color_temperature":[]}`), PlanOnly: true},
	}})
	for _, r := range b.Requests() {
		if r.Method != "PUT" {
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(r.Body, &payload) != nil {
			continue
		}
		if strings.Contains(string(payload["metadata"]), "preserved") && len(payload["palette"]) != 0 {
			t.Fatal("omitted palette sent")
		}
	}
}
