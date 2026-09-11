package hue

import (
	"fmt"
	"regexp"
	"strconv"
)

type SmartClock struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
	Second int `json:"second"`
}
type SmartStart struct {
	Kind string      `json:"kind"`
	Time *SmartClock `json:"time,omitempty"`
}
type SmartSlot struct {
	StartTime SmartStart `json:"start_time"`
	Target    Reference  `json:"target"`
}
type SmartDay struct {
	Recurrence []string    `json:"recurrence"`
	Timeslots  []SmartSlot `json:"timeslots"`
}
type SmartScene struct {
	ID                 string     `json:"id,omitempty"`
	Type               string     `json:"type,omitempty"`
	Metadata           Metadata   `json:"metadata"`
	Group              Reference  `json:"group"`
	WeekTimeslots      []SmartDay `json:"week_timeslots"`
	TransitionDuration int64      `json:"transition_duration"`
	State              string     `json:"state,omitempty"`
}

var smartClockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9]):([0-5][0-9])$`)
var smartUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

func ParseSmartStart(value string) (SmartStart, error) {
	if value == "sunset" {
		return SmartStart{Kind: "sunset"}, nil
	}
	parts := smartClockPattern.FindStringSubmatch(value)
	if parts == nil {
		return SmartStart{}, fmt.Errorf("start_time must be HH:MM:SS or sunset")
	}
	hour, _ := strconv.Atoi(parts[1])
	minute, _ := strconv.Atoi(parts[2])
	second, _ := strconv.Atoi(parts[3])
	return SmartStart{Kind: "time", Time: &SmartClock{hour, minute, second}}, nil
}
func FormatSmartStart(start SmartStart) (string, error) {
	if start.Kind == "sunset" {
		return "sunset", nil
	}
	if start.Kind != "time" || start.Time == nil {
		return "", fmt.Errorf("unsupported smart scene start time")
	}
	c := start.Time
	value := fmt.Sprintf("%02d:%02d:%02d", c.Hour, c.Minute, c.Second)
	if _, err := ParseSmartStart(value); err != nil {
		return "", err
	}
	return value, nil
}

func ValidateSmartSchedule(days []SmartDay) error {
	if len(days) == 0 {
		return fmt.Errorf("week_timeslots must not be empty")
	}
	weekdays := map[string]bool{"monday": true, "tuesday": true, "wednesday": true, "thursday": true, "friday": true, "saturday": true, "sunday": true}
	used := map[string]bool{}
	for _, day := range days {
		if len(day.Recurrence) == 0 || len(day.Timeslots) == 0 {
			return fmt.Errorf("each schedule needs recurrence days and timeslots")
		}
		for _, d := range day.Recurrence {
			if !weekdays[d] || used[d] {
				return fmt.Errorf("invalid or repeated recurrence day: %s", d)
			}
			used[d] = true
		}
		starts := map[string]bool{}
		for _, slot := range day.Timeslots {
			t, err := FormatSmartStart(slot.StartTime)
			if err != nil {
				return err
			}
			if starts[t] {
				return fmt.Errorf("repeated start_time: %s", t)
			}
			starts[t] = true
			if slot.Target.RType != "scene" || !smartUUIDPattern.MatchString(slot.Target.RID) {
				return fmt.Errorf("timeslot target must be a scene UUID")
			}
		}
	}
	return nil
}
