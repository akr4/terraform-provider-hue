package preview

import (
	"fmt"
	"math"
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
			var r, g, b float64
			if _, err := fmt.Sscanf(s.CSS, "color(srgb %f %f %f)", &r, &g, &b); err != nil {
				t.Fatal(err)
			}
			if level == 0 && (peak != 0 || r != 0 || g != 0 || b != 0) {
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

// A swatch scales the encoded color by brightness, as the V of HSV scales: at
// 50%, 20% and 0% each channel is that share of the channel at 100%. A green
// outside sRGB keeps its negative red in the CSS, scaled the same way, and the
// terminal clips it. A channel at zero is written as 0, not -0.
func TestBrightnessFromV(t *testing.T) {
	for name, setting := range map[string]map[string]any{
		"orange":             {"color_xy": map[string]any{"x": 0.56, "y": 0.38}},
		"green outside sRGB": {"color_xy": map[string]any{"x": 0.17, "y": 0.7}},
		"temperature":        {"mirek": 370.0},
		"brightness only":    {},
	} {
		swatch := func(brightness float64) (Sample, [3]float64) {
			setting["brightness"] = brightness
			s := sample(setting, true)
			var c [3]float64
			if _, err := fmt.Sscanf(s.CSS, "color(srgb %f %f %f)", &c[0], &c[1], &c[2]); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if strings.Contains(s.CSS, "-0.000000") {
				t.Errorf("%s at %g%%: %s", name, brightness, s.CSS)
			}
			return s, c
		}
		full, fullCSS := swatch(100)
		if max(full.R, full.G, full.B) != 255 {
			t.Errorf("%s at 100%%: rgb(%d %d %d)", name, full.R, full.G, full.B)
		}
		for _, brightness := range []float64{50, 20, 0} {
			s, c := swatch(brightness)
			share := brightness / 100
			got, want := []int{s.R, s.G, s.B}, []int{full.R, full.G, full.B}
			for i := range c {
				if math.Abs(c[i]-fullCSS[i]*share) > 2e-6 || math.Abs(float64(got[i])-float64(want[i])*share) > 1 {
					t.Errorf("%s at %g%%: %v and rgb%v, at 100%%: %v and rgb%v", name, brightness, c, got, fullCSS, want)
					break
				}
			}
		}
	}
	green := sample(map[string]any{"color_xy": map[string]any{"x": 0.17, "y": 0.7}, "brightness": 100.0}, true)
	if !strings.HasPrefix(green.CSS, "color(srgb -") || green.R != 0 {
		t.Errorf("the green outside sRGB is %s and rgb(%d %d %d)", green.CSS, green.R, green.G, green.B)
	}
}
