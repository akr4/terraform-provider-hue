package pull

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const groupSource = `# Keep heading
resource "hue_room" "bedroom" {
 name = "Old" # Keep name annotation
 archetype = "bedroom"
 children = ["a", "b"] # devices
 lifecycle {
  prevent_destroy = true
 }
}
`

func groupBaseline() Baseline {
	return Baseline{ID: sceneID, Name: "Old", Archetype: "bedroom", Children: []string{"a", "b"}}
}
func groupRemote(kind string) hue.Group {
	ct := "device"
	if kind == "zone" {
		ct = "light"
	}
	return hue.Group{ID: sceneID, Type: kind, Metadata: hue.Metadata{Name: "New ${literal}", Archetype: "office"}, Children: []hue.Reference{{RID: "b", RType: ct}, {RID: "c", RType: ct}}}
}
func TestGroupRoundTrip(t *testing.T) {
	for _, kind := range []string{"room", "zone"} {
		t.Run(kind, func(t *testing.T) {
			src := strings.Replace(groupSource, "hue_room", "hue_"+kind, 1)
			dir := fixture(t, src)
			c, err := PrepareGroup(dir, kind, "bedroom", groupBaseline(), groupRemote(kind))
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Edits) != 3 {
				t.Fatal(c.Edits)
			}
			for _, part := range []string{"# Keep heading", "# Keep name annotation", "# devices", "prevent_destroy = true", `"New $${literal}"`} {
				if !strings.Contains(string(c.Updated), part) {
					t.Fatal(string(c.Updated))
				}
			}
			f, d := hclsyntax.ParseConfig(c.Updated, "generated.tf", hcl.InitialPos)
			if d.HasErrors() {
				t.Fatal(d)
			}
			v, d := f.Body.(*hclsyntax.Body).Blocks[0].Body.Attributes["children"].Expr.Value(nil)
			if d.HasErrors() || v.LengthInt() != 2 {
				t.Fatal("invalid list")
			}
			backup, err := c.Write()
			if err != nil {
				t.Fatal(err)
			}
			orig, _ := os.ReadFile(backup)
			if string(orig) != src {
				t.Fatal("backup changed")
			}
			again, err := PrepareGroup(dir, kind, "bedroom", groupBaseline(), groupRemote(kind))
			if err != nil || len(again.Edits) != 0 {
				t.Fatalf("not idempotent: %v", err)
			}
		})
	}
}
func TestGroupOrderAndEmpty(t *testing.T) {
	g := groupRemote("room")
	g.Metadata = hue.Metadata{Name: "Old", Archetype: "bedroom"}
	g.Children = []hue.Reference{{RID: "b", RType: "device"}, {RID: "a", RType: "device"}}
	dir := fixture(t, groupSource)
	c, err := PrepareGroup(dir, "room", "bedroom", groupBaseline(), g)
	if err != nil || len(c.Edits) != 0 {
		t.Fatalf("order-only diff %v", err)
	}
	g.Children = nil
	c, err = PrepareGroup(dir, "room", "bedroom", groupBaseline(), g)
	if err != nil || !strings.Contains(string(c.Updated), "children = []") {
		t.Fatalf("empty list: %v", err)
	}
}
func TestGroupGuards(t *testing.T) {
	for _, src := range []string{
		strings.Replace(groupSource, `name = "Old"`, `name = var.name`, 1),
		strings.Replace(groupSource, `name = "Old"`, `name = "Local edit"`, 1),
		strings.Replace(groupSource, `["a", "b"]`, `["a", "d"]`, 1),
		strings.Replace(groupSource, `["a", "b"]`, `[data.hue_light.a.device_id]`, 1),
		strings.Replace(groupSource, `["a", "b"]`, "[\"a\", # keep child annotation\n \"b\"]", 1),
	} {
		dir := fixture(t, src)
		if _, err := PrepareGroup(dir, "room", "bedroom", groupBaseline(), groupRemote("room")); err == nil {
			t.Fatal("accepted unsafe change")
		}
		got, _ := os.ReadFile(filepath.Join(dir, "night.tf"))
		if string(got) != src {
			t.Fatal("partial write")
		}
	}
	g := groupRemote("zone")
	if _, err := PrepareGroup(fixture(t, groupSource), "room", "bedroom", groupBaseline(), g); err == nil {
		t.Fatal("wrong group type")
	}
}
func TestNewGroup(t *testing.T) {
	for _, kind := range []string{"room", "zone"} {
		childKind := "device"
		if kind == "zone" {
			childKind = "light"
		}
		raw := []byte(`{"id":"group","type":"` + kind + `","metadata":{"name":"新規","archetype":"other"},"children":[{"rid":"child","rtype":"` + childKind + `"}]}`)
		src, err := NewGroup(raw, kind, "new_group")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), `"hue_`+kind+`"`) || !strings.Contains(string(src), "prevent_destroy = true") {
			t.Fatal(string(src))
		}
		if _, err = NewGroup(raw, map[string]string{"room": "zone", "zone": "room"}[kind], "other"); err == nil {
			t.Fatal("wrong type accepted")
		}
	}
}
