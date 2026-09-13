package preview

import (
	"bytes"
	"strings"
	"testing"
)

func TestHTMLCompactRows(t *testing.T) {
	color := Sample{Exists: true, HasColor: true, CSS: "color(xyz-d65 0.3 1 0.4)", Brightness: "20%", On: "on", Color: "xy 0.3, 0.4"}
	for _, tc := range []struct {
		name                    string
		row                     Row
		swatches, before, after int
		marker                  string
	}{
		{"unchanged", Row{Before: color, After: color}, 1, 0, 0, "current"},
		{"snapshot", Row{After: color}, 1, 0, 0, "current"},
		{"changed", Row{Before: color, After: color, Changed: true}, 2, 1, 1, "comparison"},
		{"added", Row{After: color, Changed: true}, 1, 0, 1, "Added"},
		{"removed", Row{Before: color, Changed: true}, 1, 1, 0, "Removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := HTML(&out, View{Scenes: []Scene{{Rows: []Row{tc.row}}}}); err != nil {
				t.Fatal(err)
			}
			s := out.String()
			for fragment, want := range map[string]int{`class="swatch"`: tc.swatches, ">Before<": tc.before, ">Planned<": tc.after} {
				if got := strings.Count(s, fragment); got != want {
					t.Errorf("%s: got %d want %d", fragment, got, want)
				}
			}
			if !strings.Contains(s, tc.marker) {
				t.Errorf("missing %s", tc.marker)
			}
			if strings.Contains(s, "Approximate brightness") {
				t.Fatal("duplicate brightness swatch")
			}
		})
	}
}

func TestPreviewOffLights(t *testing.T) {
	for _, action := range []map[string]any{
		{"on": false},
		{"on": false, "brightness": 20.0, "color_xy": map[string]any{"x": 0.3, "y": 0.4}},
	} {
		s := sample(action, true)
		if s.HasColor {
			t.Fatal("off light has a lit color swatch")
		}
		if strings.Contains(terminalSample(s, true), "\x1b[48;2;") {
			t.Fatal("terminal renders off light as lit")
		}
		var out bytes.Buffer
		if err := HTML(&out, View{Scenes: []Scene{{Rows: []Row{{After: s}}}}}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `aria-label="Off"`) || strings.Contains(out.String(), `title="Configured color and brightness"`) {
			t.Fatal("expected off marker instead of color swatch")
		}
	}
	lit := sample(map[string]any{"on": true, "brightness": 20.0}, true)
	if !lit.HasColor {
		t.Fatal("lit brightness-only light lost its swatch")
	}
}
