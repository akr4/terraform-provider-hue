package hue

import "testing"

func TestSmartStart(t *testing.T) {
	for _, v := range []string{"00:00:00", "23:59:59", "07:30:01", "sunset"} {
		start, err := ParseSmartStart(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSmartStart(start)
		if err != nil || got != v {
			t.Fatalf("%s %s %v", v, got, err)
		}
	}
	for _, v := range []string{"24:00:00", "07:60:00", "07:00:60", "7:00", "sunrise", "-1:00:00"} {
		if _, err := ParseSmartStart(v); err == nil {
			t.Fatal(v)
		}
	}
	if got, err := FormatSmartStart(SmartStart{Kind: "sunset", Time: &SmartClock{}}); err != nil || got != "sunset" {
		t.Fatal(got, err)
	}
}
func TestSmartScheduleValidation(t *testing.T) {
	start, _ := ParseSmartStart("00:00:00")
	day := SmartDay{Recurrence: []string{"monday"}, Timeslots: []SmartSlot{{StartTime: start, Target: Reference{RID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RType: "scene"}}}}
	if err := ValidateSmartSchedule([]SmartDay{day}); err != nil {
		t.Fatal(err)
	}
	for _, days := range [][]SmartDay{nil, {{Recurrence: []string{"monday"}}}, {{Recurrence: []string{"invalid"}, Timeslots: day.Timeslots}}, {day, day}, {{Recurrence: day.Recurrence, Timeslots: append(append([]SmartSlot{}, day.Timeslots...), day.Timeslots...)}}} {
		if err := ValidateSmartSchedule(days); err == nil {
			t.Fatalf("accepted %+v", days)
		}
	}
	day.Timeslots[0].Target.RType = "smart_scene"
	if err := ValidateSmartSchedule([]SmartDay{day}); err == nil {
		t.Fatal("accepted non-scene target")
	}
}
