package pull

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func TestSmartSceneGeneration(t *testing.T) {
	group := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	first := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	start, _ := hue.ParseSmartStart("00:00:00")
	remote := hue.SmartScene{ID: id, Type: "smart_scene", Metadata: hue.Metadata{Name: "Natural"}, Group: hue.Reference{RID: group, RType: "room"}, TransitionDuration: 60000, WeekTimeslots: []hue.SmartDay{{Recurrence: []string{"monday", "sunday"}, Timeslots: []hue.SmartSlot{{StartTime: start, Target: hue.Reference{RID: first, RType: "scene"}}}}}}
	raw, _ := json.Marshal(remote)
	generated, err := NewSmartScene(raw, "natural")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "00:00:00") {
		t.Fatal(string(generated))
	}
}
