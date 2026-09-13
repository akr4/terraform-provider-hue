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
