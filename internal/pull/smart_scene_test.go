package pull

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func TestSmartSceneSync(t *testing.T) {
	dir := t.TempDir()
	group := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	first := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	next := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	source := `resource "hue_smart_scene" "natural" {
 name = "Natural"
 group = "` + group + `"
 transition_duration = 60000
 week_timeslots = [{
  recurrence = ["sunday", "monday"]
  timeslots = [{
   start_time = "00:00:00"
   scene = hue_scene.old.id # keep this comment
  }]
 }]
}
`
	if err := os.WriteFile(dir+"/smart.tf", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	start, _ := hue.ParseSmartStart("00:00:00")
	remote := hue.SmartScene{ID: id, Type: "smart_scene", Metadata: hue.Metadata{Name: "Natural"}, Group: hue.Reference{RID: group, RType: "room"}, TransitionDuration: 60000, WeekTimeslots: []hue.SmartDay{{Recurrence: []string{"monday", "sunday"}, Timeslots: []hue.SmartSlot{{StartTime: start, Target: hue.Reference{RID: first, RType: "scene"}}}}}}
	raw, _ := json.Marshal(remote)
	generated, err := NewSmartScene(raw, "natural")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := CanonicalAttributes(generated)
	if err != nil {
		t.Fatal(err)
	}
	ctx := StateContext([]ManagedResource{{Address: "hue_scene.old", ID: first, Kind: "scene", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"` + first + `"`)}}, {Address: "hue_scene.next", ID: next, Kind: "scene", Attributes: map[string]json.RawMessage{"id": json.RawMessage(`"` + next + `"`)}}}, "")
	same, err := PrepareSync(dir, "smart_scene", "natural", baseline, generated, ctx)
	if err != nil || len(same.Edits) != 0 {
		t.Fatalf("%+v %v", same, err)
	}
	remote.WeekTimeslots[0].Timeslots[0].Target.RID = next
	raw, _ = json.Marshal(remote)
	generated, err = NewSmartScene(raw, "natural")
	if err != nil {
		t.Fatal(err)
	}
	change, err := PrepareSync(dir, "smart_scene", "natural", baseline, generated, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(change.Updated), "hue_scene.next.id # keep this comment") {
		t.Fatal(string(change.Updated))
	}
	if _, err := CheckRemovals(map[string]string{"": dir}, nil, []ManagedResource{{Address: "hue_scene.old", ID: first, Kind: "scene"}}); err == nil {
		t.Fatal("allowed removing a scene referenced by a smart scene")
	}
	// Preserve local edits and detect conflicts against the original pull baseline.
	if err = os.WriteFile(dir+"/smart.tf", []byte(strings.Replace(source, "00:00:00", "01:00:00", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	remote.WeekTimeslots[0].Timeslots[0].StartTime, _ = hue.ParseSmartStart("02:00:00")
	raw, _ = json.Marshal(remote)
	generated, _ = NewSmartScene(raw, "natural")
	if _, err = PrepareSync(dir, "smart_scene", "natural", baseline, generated, ctx); err == nil {
		t.Fatal("missed conflicting edit")
	}
}
