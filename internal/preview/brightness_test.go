package preview

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfiguredBrightness(t *testing.T) {
	for _, color := range []map[string]any{
		{"color_xy": map[string]any{"x": 0.3127, "y": 0.3290}},
		{"mirek": 400.0},
		{},
	} {
		previous := 256
		for _, level := range []float64{100, 50, 10, 0.79, 0} {
			color["brightness"] = level
			s := sample(color, true)
			if !s.HasColor {
				t.Fatal(s)
			}
			peak := max(s.R, s.G, s.B)
			if peak >= previous {
				t.Fatalf("brightness %g did not reduce display value: %+v", level, s)
			}
			previous = peak
			var x, y, z float64
			if _, err := fmt.Sscanf(s.CSS, "color(xyz-d65 %f %f %f)", &x, &y, &z); err != nil {
				t.Fatal(err)
			}
			if level == 0 && (peak != 0 || x != 0 || y != 0 || z != 0) {
				t.Fatal("zero brightness must be black")
			}
			if !strings.Contains(terminalSample(s, true), fmt.Sprintf("[48;2;%d;%d;%dm", s.R, s.G, s.B)) {
				t.Fatal("terminal does not use adjusted color")
			}
		}
	}
	for _, brightness := range []any{nil, pending, redacted, -1.0, 101.0} {
		s := sample(map[string]any{"brightness": brightness, "color_xy": map[string]any{"x": 0.3, "y": 0.4}}, true)
		if s.HasColor || s.Notice == "" {
			t.Fatalf("invented brightness: %+v", s)
		}
	}
}
