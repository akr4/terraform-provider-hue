package pull

import (
	"os"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func TestColorModeSwitch(t *testing.T) {
	for _, mode := range []string{"mirek-to-xy", "kelvin-to-xy", "xy-to-mirek"} {
		t.Run(mode, func(t *testing.T) {
			src := source
			b := baseline()
			r := scene()
			targetKey, removed := "color_xy", "mirek"
			if mode == "xy-to-mirek" {
				src = strings.Replace(src, "mirek = 346", "color_xy = { x = 0.3, y = 0.4 } # preserve", 1)
				a := b.Actions[lightID]
				a.Mirek = nil
				a.XY = &hue.XY{X: 0.3, Y: 0.4}
				b.Actions[lightID] = a
				r.Actions[0].Action.ColorTemperature.Mirek = 400
				targetKey, removed = "mirek", "color_xy"
			} else {
				if mode == "kelvin-to-xy" {
					src = strings.Replace(src, "mirek = 346", "kelvin = 2890", 1)
					a := b.Actions[lightID]
					k := int64(2890)
					a.Kelvin = &k
					b.Actions[lightID] = a
					removed = "kelvin"
				}
				r.Actions[0].Action.ColorTemperature = nil
				r.Actions[0].Action.Color = &hue.ActionColor{XY: hue.XY{X: 0.2, Y: 0.5}}
			}
			dir := fixture(t, src)
			c, err := Prepare(dir, "night", b, r)
			if err != nil {
				t.Fatal(err)
			}
			f, d := hclsyntax.ParseConfig(c.Updated, "out.tf", hcl.InitialPos)
			if d.HasErrors() {
				t.Fatal(d)
			}
			attrs := f.Body.(*hclsyntax.Body).Blocks[0].Body.Attributes
			actions, err := object(attrs["actions"].Expr)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := object(actions[lightID])
			if err != nil {
				t.Fatal(err)
			}
			if fields[targetKey] == nil || fields[removed] != nil {
				t.Fatal(string(c.Updated))
			}
			if !strings.Contains(string(c.Updated), "# Keep this comment too.") {
				t.Fatal("comment lost")
			}
			if mode == "xy-to-mirek" && !strings.Contains(string(c.Updated), "# preserve") {
				t.Fatal("trailing comment lost")
			}
			if _, err = c.Write(); err != nil {
				t.Fatal(err)
			}
			again, err := Prepare(dir, "night", b, r)
			if err != nil || len(again.Edits) != 0 {
				t.Fatalf("not idempotent: %v", err)
			}
		})
	}
}
func TestColorModeGuards(t *testing.T) {
	for _, replacement := range []string{"mirek = var.temperature", "mirek = 400", "mirek = 300 + 46"} {
		src := strings.Replace(source, "mirek = 346", replacement, 1)
		r := scene()
		r.Actions[0].Action.ColorTemperature = nil
		r.Actions[0].Action.Color = &hue.ActionColor{XY: hue.XY{X: 0.3, Y: 0.4}}
		dir := fixture(t, src)
		if _, err := Prepare(dir, "night", baseline(), r); err == nil {
			t.Fatal("accepted " + replacement)
		}
		got, _ := os.ReadFile(dir + "/night.tf")
		if string(got) != src {
			t.Fatal("partial write")
		}
	}
	for _, xy := range []string{`{ x = 0.8, y = 0.4 }`, `{ x = var.x, y = 0.4 }`, "{ x = 0.3, # important\n y = 0.4 }"} {
		src := strings.Replace(source, "mirek = 346", "color_xy = "+xy, 1)
		b := baseline()
		a := b.Actions[lightID]
		a.Mirek = nil
		a.XY = &hue.XY{X: 0.3, Y: 0.4}
		b.Actions[lightID] = a
		if _, err := Prepare(fixture(t, src), "night", b, scene()); err == nil {
			t.Fatal("accepted unsafe color")
		}
	}
}
func TestDecodeNullTemperature(t *testing.T) {
	s, err := DecodeScene([]byte(`{"actions":[{"action":{"color_temperature":{"mirek":null},"color":{"xy":{"x":0.2,"y":0.5}}}}]}`))
	if err != nil || s.Actions[0].Action.ColorTemperature != nil || s.Actions[0].Action.Color == nil {
		t.Fatalf("%+v %v", s, err)
	}
}
