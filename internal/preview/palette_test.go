package preview

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPalettePreview(t *testing.T) {
	p := `{"color":[{"color":{"xy":{"x":0.16,"y":0.1}},"dimming":{"brightness":10}}],"color_temperature":[{"color_temperature":{"mirek":400},"dimming":{"brightness":8}}],"dimming":[{"brightness":8}],"effects":[{"secret":"never-render"}]}`
	rows := paletteRows(nil, p, false)
	if len(rows) != 4 || !rows[0].After.HasColor || rows[1].After.Color != "2500 K" || rows[2].After.Brightness != "8%" {
		t.Fatal(rows)
	}
	if rows[0].After.On != "" {
		t.Fatal("palette has on/off status")
	}
	changed := paletteRows(p, strings.Replace(p, `"brightness":10`, `"brightness":5`, 1), true)
	if !changed[0].Changed || changed[1].Changed || changed[2].Changed {
		t.Fatal(changed)
	}
	removed := paletteRows(p, nil, true)
	if len(removed) != 4 || !removed[0].Changed || removed[0].After.Exists {
		t.Fatal(removed)
	}
	reordered := paletteRows(`{"dimming":[{"brightness":8},{"brightness":3}]}`, `{"dimming":[{"brightness":3},{"brightness":8}]}`, true)
	if !reordered[0].Changed || !reordered[1].Changed {
		t.Fatal("order ignored")
	}
	var out bytes.Buffer
	if err := HTML(&out, View{Scenes: []Scene{{Palette: changed}}}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, ">Palette<") || strings.Contains(html, "never-render") {
		t.Fatal(html)
	}
	if strings.Count(html, ">Before<") != 1 {
		t.Fatal("unchanged palette duplicated")
	}
	out.Reset()
	if err := Terminal(&out, View{Scenes: []Scene{{Changed: true, Palette: changed}}}, false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Palette · Color 1") || strings.Contains(out.String(), "Palette · Temperature") {
		t.Fatal(out.String())
	}

	for _, flag := range []string{"sensitive", "unknown", "malformed"} {
		change := map[string]any{"actions": []string{"create"}, "after": map[string]any{"name": "test", "palette": p}}
		switch flag {
		case "sensitive":
			change["after_sensitive"] = map[string]any{"palette": true}
		case "unknown":
			change["after_unknown"] = map[string]any{"palette": true}
		case "malformed":
			change["after"].(map[string]any)["palette"] = "not-json"
		}
		raw, _ := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": []any{map[string]any{"address": "hue_scene.test", "type": "hue_scene", "change": change}}})
		v, err := Decode(bytes.NewReader(raw), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Scenes[0].Palette) != 1 || v.Scenes[0].Palette[0].After.HasColor || v.Scenes[0].Palette[0].After.Notice == "" {
			t.Fatal(v)
		}
	}
}
