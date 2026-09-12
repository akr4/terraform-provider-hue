package preview

import (
	"bytes"
	"strings"
	"testing"
)

const fixture = `{"format_version":"1.2","terraform_version":"1.10.5","planned_values":{"root_module":{"child_modules":[{"resources":[{"address":"module.room.hue_scene.steady","type":"hue_scene","values":{"name":"Steady","group":"room","actions":{"light":{"color_xy":{"x":0.3,"y":0.4},"brightness":20,"on":true}}}}]}]}},"resource_changes":[{"address":"module.room.hue_scene.changed","type":"hue_scene","change":{"actions":["update"],"before":{"name":"Evening","group":"room","actions":{"light":{"color_xy":{"x":0.4,"y":0.4},"brightness":20,"on":true}}},"after":{"name":"Evening","group":"room","actions":{"light":{"color_xy":{"x":0.16,"y":0.1},"brightness":3,"on":false},"new":{"color_xy":null,"brightness":null}}},"after_unknown":{"actions":{"new":{"color_xy":true,"brightness":true}}}}},{"address":"hue_scene.deleted","type":"hue_scene","change":{"actions":["delete"],"before":{"name":"Gone","actions":{"warm":{"mirek":400,"brightness":10}}},"after":null}}]}`

func TestPlanPreview(t *testing.T) {
	names := strings.NewReader(`{"data":[{"id":"light","type":"light","metadata":{"name":"Desk"}},{"id":"room","type":"room","metadata":{"name":"Office"}}]}`)
	v, err := Decode(strings.NewReader(fixture), names)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Scenes) != 3 {
		t.Fatal(v)
	}
	var out bytes.Buffer
	if err = Terminal(&out, v, false, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Desk", "Office", "20%", "3%", "\x1b[48;2;", pending, "Gone"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out.String(), "Steady") {
		t.Fatal("unchanged included in default diff")
	}
	out.Reset()
	if err = HTML(&out, v); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Steady", "color(xyz-d65", "background:black", "Changed scenes only", "known after apply"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(out.String(), "ZgotmplZ") {
		t.Fatal("template rejected generated CSS")
	}
}
func TestPreviewSensitiveAndEscaping(t *testing.T) {
	raw := `{"format_version":"1.2","planned_values":{"root_module":{}},"resource_changes":[{"address":"hue_scene.<script>alert(1)</script>","type":"hue_scene","change":{"actions":["create"],"after":{"name":"<img src=x onerror=alert(1)>","group":"hidden-group","actions":{"light":{"color_xy":{"x":0.3,"y":0.4},"brightness":73.12345,"on":true,"effects":"do-not-leak"}},"palette":"secret-palette"},"after_sensitive":{"group":true,"actions":{"light":{"brightness":true,"effects":true}}}}}]}`
	v, e := Decode(strings.NewReader(raw), nil)
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e = HTML(&b, v); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"hidden-group", "73.12345", "do-not-leak", "secret-palette", "<img src=x", "<script>alert"} {
		if strings.Contains(b.String(), secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
	if !strings.Contains(b.String(), "&lt;img") || !strings.Contains(b.String(), redacted) {
		t.Fatal("missing escaped or sensitive marker")
	}
	b.Reset()
	if e = Terminal(&b, v, true, false); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(b.String(), "73.12345") || strings.Contains(b.String(), "\x1b") {
		t.Fatal("sensitive or ANSI leak")
	}
}
func TestPreviewInputAndState(t *testing.T) {
	for _, raw := range []string{`{}`, `{"resources":[]}`, `{"format_version":"2.0","planned_values":{}}`, fixture + `{}`, `{`} {
		if _, e := Decode(strings.NewReader(raw), nil); e == nil {
			t.Fatalf("accepted invalid input %s", raw)
		}
	}
	raw := `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"hue_scene.s","type":"hue_scene","values":{"name":"Snapshot","actions":{}},"sensitive_values":true}]}}}`
	v, e := Decode(strings.NewReader(raw), nil)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(v.Source, "State snapshot") || v.Scenes[0].Name != redacted {
		t.Fatal(v)
	}
}
func TestColorSamples(t *testing.T) {
	for _, tc := range []struct {
		value    map[string]any
		color    bool
		contains string
	}{
		{map[string]any{"color_xy": map[string]any{"x": 0.3, "y": 0.0}}, false, "unavailable"},
		{map[string]any{"color_xy": pending}, false, pending},
		{map[string]any{"mirek": 400.0, "brightness": 20.0}, true, "2500 K"},
		{map[string]any{"brightness": 20.0}, true, "brightness only"},
		{map[string]any{"color_xy": redacted}, false, redacted},
	} {
		s := sample(tc.value, true)
		if s.HasColor != tc.color || !strings.Contains(s.Text, tc.contains) {
			t.Fatalf("%+v", s)
		}
	}
}
