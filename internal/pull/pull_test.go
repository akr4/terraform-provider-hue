package pull

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

const sceneID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const lightID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const source = `# 夜間の設定
resource "hue_scene" "night" {
  name = "深夜"
  group = hue_room.bedroom.id # Keep the reference.
  actions = {
    "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" = {
      on = true
      brightness = 20 # Keep this comment too.
      mirek = 346
    }
  }
}
`

func baseline() Baseline {
	var b Baseline
	_ = json.Unmarshal([]byte(`{"id":"`+sceneID+`","group":"group-id","actions":{"`+lightID+`":{"brightness":20,"on":true,"mirek":346}}}`), &b)
	return b
}
func scene() hue.Scene {
	return hue.Scene{ID: sceneID, Group: hue.Reference{RID: "group-id"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: lightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, ColorTemperature: &hue.Temperature{Mirek: 346}, Dimming: &hue.Dimming{Brightness: 10}}}}}
}
func fixture(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "night.tf"), []byte(src), 0640); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestBrightnessRoundTrip(t *testing.T) {
	dir := fixture(t, source)
	change, err := Prepare(dir, "night", baseline(), scene())
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Replace(source, "brightness = 20", "brightness = 10", 1)
	if string(change.Updated) != expected {
		t.Fatalf("unexpected edit: %s", change.Updated)
	}
	before, _ := os.ReadFile(change.Path)
	if string(before) != source {
		t.Fatal("preview modified file")
	}
	backup, err := change.Write()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(backup)
	if string(saved) != source {
		t.Fatal("backup lost original")
	}
	info, _ := os.Stat(change.Path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("mode changed")
	}
	again, err := Prepare(dir, "night", baseline(), scene())
	if err != nil || len(again.Edits) != 0 {
		t.Fatalf("not idempotent: %v", err)
	}
}
func TestRefusesUnsafeChanges(t *testing.T) {
	for _, value := range []string{"var.brightness", "10 + 10", "null", "30"} {
		t.Run(value, func(t *testing.T) {
			src := strings.Replace(source, "brightness = 20", "brightness = "+value, 1)
			dir := fixture(t, src)
			if _, err := Prepare(dir, "night", baseline(), scene()); err == nil {
				t.Fatal("accepted expression/local edit")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "night.tf"))
			if string(got) != src {
				t.Fatal("modified on error")
			}
		})
	}
	for _, kind := range []string{"group", "id", "missing", "duplicate", "brightness"} {
		t.Run(kind, func(t *testing.T) {
			s := scene()
			switch kind {
			case "group":
				s.Group.RID = "different"
			case "id":
				s.ID = "different"
			case "missing":
				s.Actions = nil
			case "duplicate":
				s.Actions = append(s.Actions, s.Actions[0])
			case "brightness":
				s.Actions[0].Action.Dimming = nil
			}
			if _, err := Prepare(fixture(t, source), "night", baseline(), s); err == nil {
				t.Fatal("accepted incompatible scene")
			}
		})
	}
}
func TestStaleFileAndSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := fixture(t, source)
		c, err := Prepare(dir, "night", baseline(), scene())
		if err != nil {
			t.Fatal(err)
		}
		if symlink {
			os.Rename(c.Path, c.Path+".original")
			os.Symlink(c.Path+".original", c.Path)
		} else {
			os.WriteFile(c.Path, []byte(source+"# new edit\n"), 0600)
		}
		if _, err = c.Write(); err == nil {
			t.Fatal("overwrote stale file/symlink")
		}
	}
}
func TestStateIdentity(t *testing.T) {
	state := `{"resources":[{"mode":"managed","type":"hue_scene","name":"night","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"` + sceneID + `","group":"group-id"}}]}]}`
	if _, err := State([]byte(state), "hue_scene.night"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`{`, strings.Replace(state, `"night"`, `"other"`, 1), strings.Replace(state, `"attributes":`, `"index_key":0,"attributes":`, 1), strings.Replace(state, `hue\"]`, `hue\"].other`, 1)} {
		if _, err := State([]byte(s), "hue_scene.night"); err == nil {
			t.Fatal("accepted unsupported state")
		}
	}
}
func TestDecimals(t *testing.T) {
	src := strings.Replace(source, "brightness = 20", "brightness = 53.75", 1)
	b := baseline()
	v := b.Actions[lightID]
	n := 53.75
	v.Brightness = &n
	b.Actions[lightID] = v
	s := scene()
	s.Actions[0].Action.Dimming.Brightness = 12.34
	c, err := Prepare(fixture(t, src), "night", b, s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(c.Updated, []byte("brightness = 12.34 #")) {
		t.Fatal(string(c.Updated))
	}
}

func TestMultipleEdits(t *testing.T) {
	src := strings.Replace(source, "  actions = {", "  actions = {\n    second = { brightness = 20, on = true, mirek = 346 }", 1)
	b := baseline()
	b.Actions["second"] = b.Actions[lightID]
	remote := scene()
	second := remote.Actions[0]
	second.Target.RID = "second"
	remote.Actions = append(remote.Actions, second)
	c, err := Prepare(fixture(t, src), "night", b, remote)
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Updated) != strings.ReplaceAll(src, "brightness = 20", "brightness = 10") {
		t.Fatal(string(c.Updated))
	}
}

func TestOverrideRejected(t *testing.T) {
	dir := fixture(t, source)
	if err := os.WriteFile(filepath.Join(dir, "override.tf"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(dir, "night", baseline(), scene()); err == nil {
		t.Fatal("accepted override")
	}
}

func TestActionFields(t *testing.T) {
	src := strings.Replace(source, "      mirek = 346", "      mirek = 346 # temperature\n      color_xy = { x = 0.3, y = 0.4 } # color", 1)
	b := baseline()
	a := b.Actions[lightID]
	a.XY = &hue.XY{X: 0.3, Y: 0.4}
	b.Actions[lightID] = a
	remote := scene()
	remote.Actions[0].Action.On.On = false
	remote.Actions[0].Action.ColorTemperature.Mirek = 400
	remote.Actions[0].Action.Color = &hue.ActionColor{XY: hue.XY{X: 0.5, Y: 0.2}}
	c, err := Prepare(fixture(t, src), "night", b, remote)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("on = true", "on = false", "brightness = 20", "brightness = 10", "mirek = 346", "mirek = 400", "x = 0.3", "x = 0.5", "y = 0.4", "y = 0.2").Replace(src)
	if string(c.Updated) != want {
		t.Fatalf("unexpected update: %s", c.Updated)
	}
}
func TestNewFieldExpressionAndConflict(t *testing.T) {
	for _, replacement := range []string{"on = var.enabled", "on = false", "mirek = 300 + 46", "mirek = 400"} {
		src := source
		if strings.HasPrefix(replacement, "on") {
			src = strings.Replace(src, "on = true", replacement, 1)
		} else {
			src = strings.Replace(src, "mirek = 346", replacement, 1)
		}
		r := scene()
		r.Actions[0].Action.ColorTemperature.Mirek = 450
		if _, err := Prepare(fixture(t, src), "night", baseline(), r); err == nil {
			t.Fatalf("accepted %s", replacement)
		}
	}
}
func TestKelvin(t *testing.T) {
	src := strings.Replace(source, "mirek = 346", "kelvin = 2890", 1)
	b := baseline()
	a := b.Actions[lightID]
	k := int64(2890)
	a.Kelvin = &k
	b.Actions[lightID] = a
	r := scene()
	r.Actions[0].Action.ColorTemperature.Mirek = 400
	c, err := Prepare(fixture(t, src), "night", b, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.Updated), "kelvin = 2500") {
		t.Fatal(string(c.Updated))
	}
}

func TestColorGuards(t *testing.T) {
	for _, field := range []string{
		`color_hex = "#ff0000"`,
		`color_xy = var.color`,
		`color_xy = { x = var.x, y = 0.4 }`,
		`color_xy = { x = 0.8, y = 0.4 }`,
	} {
		t.Run(field, func(t *testing.T) {
			src := strings.Replace(source, "      mirek = 346", "      mirek = 346\n      "+field, 1)
			b := baseline()
			a := b.Actions[lightID]
			a.XY = &hue.XY{X: 0.3, Y: 0.4}
			b.Actions[lightID] = a
			r := scene()
			r.Actions[0].Action.Color = &hue.ActionColor{XY: hue.XY{X: 0.5, Y: 0.2}}
			dir := fixture(t, src)
			if _, err := Prepare(dir, "night", b, r); err == nil {
				t.Fatal("accepted unsupported/conflicting color")
			}
			current, _ := os.ReadFile(filepath.Join(dir, "night.tf"))
			if string(current) != src {
				t.Fatal("partial update")
			}
		})
	}
}

func TestXYPrecisionNoise(t *testing.T) {
	for _, tc := range []struct {
		name           string
		before, actual float64
		changed        bool
	}{
		{"reported", 0.5608999, 0.5609, false},
		{"within tolerance", 0.5609, 0.5619, false},
		{"real change", 0.5609, 0.5621, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(source, "      mirek = 346", fmt.Sprintf("      mirek = 346\n      color_xy = { x = %.7f, y = 0.4 }", tc.before), 1)
			b := baseline()
			a := b.Actions[lightID]
			a.XY = &hue.XY{X: tc.before, Y: 0.4}
			b.Actions[lightID] = a
			r := scene()
			r.Actions[0].Action.Dimming.Brightness = 20
			r.Actions[0].Action.Color = &hue.ActionColor{XY: hue.XY{X: tc.actual, Y: 0.4}}
			c, err := Prepare(fixture(t, src), "night", b, r)
			if err != nil {
				t.Fatal(err)
			}
			if (len(c.Edits) > 0) != tc.changed {
				t.Fatalf("unexpected edits: %+v", c.Edits)
			}
			if !tc.changed && string(c.Updated) != src {
				t.Fatal("precision-only edit modified source")
			}
		})
	}
}
