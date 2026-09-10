package hue

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActionNullableTemperature(t *testing.T) {
	for _, tc := range []struct {
		name, temperature string
		present           bool
		value             int64
	}{
		{"null mirek", `{"mirek":null}`, false, 0},
		{"missing mirek", `{}`, false, 0},
		{"null object", `null`, false, 0},
		{"real value", `{"mirek":346}`, true, 346},
		{"explicit zero", `{"mirek":0}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s Scene
			raw := `{"actions":[{"action":{"on":{"on":false},"dimming":{"brightness":0},"color_temperature":` + tc.temperature + `,"color":{"xy":{"x":0.3,"y":0.4}}}}]}`
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				t.Fatal(err)
			}
			a := s.Actions[0].Action
			if (a.ColorTemperature != nil) != tc.present {
				t.Fatal("null became a numeric temperature")
			}
			if tc.present && a.ColorTemperature.Mirek != tc.value {
				t.Fatal(a)
			}
			if a.On == nil || a.On.On || a.Dimming == nil || a.Dimming.Brightness != 0 || a.Color == nil {
				t.Fatal("other fields changed")
			}
			encoded, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "color_temperature") != tc.present {
				t.Fatal(string(encoded))
			}
		})
	}
}
