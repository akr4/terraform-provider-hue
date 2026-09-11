package pull

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// NewSmartScene projects API data solely for comparison/editing existing HCL.
// New resources are represented by import blocks, not generated definitions.
func NewSmartScene(raw json.RawMessage, name string) ([]byte, error) {
	if !hclsyntax.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid resource name")
	}
	var scene hue.SmartScene
	if err := json.Unmarshal(raw, &scene); err != nil {
		return nil, err
	}
	if scene.ID == "" || scene.Metadata.Name == "" || (scene.Group.RType != "room" && scene.Group.RType != "zone") {
		return nil, fmt.Errorf("invalid smart scene identity/group")
	}
	if err := hue.ValidateSmartSchedule(scene.WeekTimeslots); err != nil {
		return nil, err
	}
	f := hclwrite.NewEmptyFile()
	b := f.Body().AppendNewBlock("resource", []string{"hue_smart_scene", name}).Body()
	b.SetAttributeValue("name", cty.StringVal(scene.Metadata.Name))
	b.SetAttributeValue("group", cty.StringVal(scene.Group.RID))
	b.SetAttributeValue("transition_duration", cty.NumberIntVal(scene.TransitionDuration))
	days := []cty.Value{}
	for _, day := range scene.WeekTimeslots {
		weekdays := append([]string{}, day.Recurrence...)
		sort.Strings(weekdays)
		recurrence := []cty.Value{}
		for _, w := range weekdays {
			recurrence = append(recurrence, cty.StringVal(w))
		}
		slots := []cty.Value{}
		for _, slot := range day.Timeslots {
			start, err := hue.FormatSmartStart(slot.StartTime)
			if err != nil {
				return nil, err
			}
			slots = append(slots, cty.ObjectVal(map[string]cty.Value{"start_time": cty.StringVal(start), "scene": cty.StringVal(slot.Target.RID)}))
		}
		days = append(days, cty.ObjectVal(map[string]cty.Value{"recurrence": cty.TupleVal(recurrence), "timeslots": cty.TupleVal(slots)}))
	}
	b.SetAttributeValue("week_timeslots", cty.TupleVal(days))
	return f.Bytes(), nil
}
