package preview

import (
	"strings"
	"testing"
)

// Effects and gradients are flagged as not simulated. An effects_v2 that stops
// an effect is not an effect.
func TestActionExtras(t *testing.T) {
	for name, c := range map[string]struct {
		action map[string]any
		want   string
	}{
		"gradient":            {map[string]any{"gradient": map[string]any{"mode": "interpolated_palette", "points": []any{}}}, "gradient present"},
		"deprecated effects":  {map[string]any{"effects": `{"effect":"fire"}`}, "effects present"},
		"effect":              {map[string]any{"effects_v2": map[string]any{"effect": "candle", "parameters": nil}}, "effects_v2 present"},
		"effect from 0.1":     {map[string]any{"effects_v2": `{"action":{"effect":"candle"}}`}, "effects_v2 present"},
		"effect unknown":      {map[string]any{"effects_v2": pending}, "effects_v2 present"},
		"effect name unknown": {map[string]any{"effects_v2": map[string]any{"effect": pending}}, "effects_v2 present"},
		"no effect":           {map[string]any{"effects_v2": map[string]any{"effect": "no_effect", "parameters": nil}}, ""},
		"no effect from 0.1":  {map[string]any{"effects_v2": `{"action":{"effect":"no_effect"}}`}, ""},
		"transition":          {map[string]any{"dynamics": map[string]any{"duration": 800.0}}, ""},
	} {
		c.action["on"], c.action["brightness"] = true, 60.0
		s := sample(c.action, true)
		if s.Extra != c.want {
			t.Errorf("%s: flagged %q, want %q", name, s.Extra, c.want)
		}
		if simulated := !strings.Contains(s.Notice, "not simulated"); simulated != (c.want == "") {
			t.Errorf("%s: notice %q", name, s.Notice)
		}
	}
}
