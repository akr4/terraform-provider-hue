package preview

import (
	"encoding/json"
	"fmt"
)

// Decode only supported palette fields; never render the raw JSON. Plans
// from provider 0.2.0 on hold the palette as an object, and earlier plans as a
// JSON string in the API's form.
func paletteSamples(raw any) []Row {
	if raw == nil {
		return nil
	}
	if text, ok := raw.(string); ok {
		if special(text) {
			return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: text}}}
		}
		var data map[string]any
		if json.Unmarshal([]byte(text), &data) != nil || data == nil {
			return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: "Invalid palette JSON"}}}
		}
		raw = apiPalette(data)
	}
	data, ok := raw.(map[string]any)
	if !ok {
		return []Row{{Name: "Palette", After: Sample{Exists: true, Notice: "Invalid palette"}}}
	}
	rows := []Row{}
	for _, category := range []struct{ key, label string }{
		{"color", "Color"}, {"color_temperature", "Temperature"}, {"dimming", "Brightness"}, {"effects", "Effects"},
	} {
		raw := data[category.key]
		if raw == nil {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			notice := "Invalid palette entries"
			if text, ok := raw.(string); ok && special(text) {
				notice = text
			}
			rows = append(rows, Row{ID: category.key, Name: category.label, After: Sample{Exists: true, Notice: notice}})
			continue
		}
		for i, entry := range entries {
			id := fmt.Sprintf("%s:%d", category.key, i)
			row := Row{ID: id, Name: fmt.Sprintf("%s %d", category.label, i+1)}
			obj, ok := entry.(map[string]any)
			if text, isText := entry.(string); isText && special(text) {
				row.After = Sample{Exists: true, Notice: text}
			} else if !ok {
				row.After = Sample{Exists: true, Notice: "Invalid palette entry"}
			} else if category.key == "effects" {
				row.After = Sample{Exists: true, Notice: "Effect present; appearance is not simulated"}
			} else {
				values := map[string]any{"brightness": obj["brightness"]}
				for key, value := range map[string]string{"color": "color_xy", "color_temperature": "mirek"} {
					if category.key == key {
						values[value] = obj[value]
						if values[value] == nil {
							values[value] = pending
						}
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

// apiPalette rewrites a palette in the API's form into the form of the
// palette attribute, which flattens wrappers that hold one field. Effects of
// both lists are effects.
func apiPalette(data map[string]any) map[string]any {
	out := map[string]any{}
	for _, category := range []struct{ key, wrapper, field, name string }{
		{"color", "color", "xy", "color_xy"}, {"color_temperature", "color_temperature", "mirek", "mirek"}, {"dimming", "", "", ""}, {"effects", "", "", ""}, {"effects_v2", "", "", ""},
	} {
		raw, exists := data[category.key]
		if !exists {
			continue
		}
		key := category.key
		if key == "effects_v2" {
			key = "effects"
		}
		entries, ok := raw.([]any)
		if !ok {
			out[key] = raw
			continue
		}
		list, _ := out[key].([]any)
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok || category.wrapper == "" {
				list = append(list, entry)
				continue
			}
			flat := map[string]any{}
			if d, ok := obj["dimming"].(map[string]any); ok {
				flat["brightness"] = d["brightness"]
			}
			if c, ok := obj[category.wrapper].(map[string]any); ok {
				flat[category.name] = c[category.field]
			}
			list = append(list, flat)
		}
		out[key] = list
	}
	return out
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
