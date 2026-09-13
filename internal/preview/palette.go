package preview

import (
	"encoding/json"
	"fmt"
)

// Decode only supported palette fields; never render the raw JSON.
func paletteSamples(raw any) []Row {
	if raw == nil {
		return nil
	}
	text, ok := raw.(string)
	if !ok {
		return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: "Invalid palette JSON"}}}
	}
	if special(text) {
		return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: text}}}
	}
	var data map[string]any
	if json.Unmarshal([]byte(text), &data) != nil || data == nil {
		return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: "Invalid palette JSON"}}}
	}
	rows := []Row{}
	for _, category := range []struct{ key, label string }{
		{"color", "Color"}, {"color_temperature", "Temperature"}, {"dimming", "Brightness"}, {"effects", "Effects"}, {"effects_v2", "Effects v2"},
	} {
		raw, exists := data[category.key]
		if !exists {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			rows = append(rows, Row{ID: category.key, Name: category.label, After: Sample{Exists: true, Notice: "Invalid palette entries"}})
			continue
		}
		for i, entry := range entries {
			id := fmt.Sprintf("%s:%d", category.key, i)
			row := Row{ID: id, Name: fmt.Sprintf("%s %d", category.label, i+1)}
			obj, ok := entry.(map[string]any)
			if !ok {
				row.After = Sample{Exists: true, Notice: "Invalid palette entry"}
			} else if category.key == "effects" || category.key == "effects_v2" {
				row.After = Sample{Exists: true, Notice: "Effect present; appearance is not simulated"}
			} else {
				values := map[string]any{}
				if category.key == "dimming" {
					values["brightness"] = obj["brightness"]
				} else if d, ok := obj["dimming"].(map[string]any); ok {
					values["brightness"] = d["brightness"]
				}
				if category.key == "color" {
					c, _ := obj["color"].(map[string]any)
					if c["xy"] == nil {
						values["color_xy"] = pending
					} else {
						values["color_xy"] = c["xy"]
					}
				}
				if category.key == "color_temperature" {
					c, _ := obj["color_temperature"].(map[string]any)
					if c["mirek"] == nil {
						values["mirek"] = pending
					} else {
						values["mirek"] = c["mirek"]
					}
				}
				row.After = sample(values, true)
				row.After.On = ""
				row.After.Text = fmt.Sprintf("%s · %s", row.After.Color, row.After.Brightness)
			}
			rows = append(rows, row)
		}
	}
	return rows
}
func paletteRows(before, after any, changed bool) []Row {
	old, next := paletteSamples(before), paletteSamples(after)
	rows := []Row{}
	used := map[string]bool{}
	for _, r := range next {
		used[r.ID] = true
		for _, o := range old {
			if o.ID == r.ID {
				r.Before = o.After
				break
			}
		}
		r.Changed = changed && !same(r.Before, r.After)
		rows = append(rows, r)
	}
	if changed {
		for _, o := range old {
			if !used[o.ID] {
				rows = append(rows, Row{ID: o.ID, Name: o.Name, Before: o.After, Changed: true})
			}
		}
	}
	return rows
}
