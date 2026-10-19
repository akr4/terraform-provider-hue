package preview

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	huecolor "github.com/akr4/terraform-provider-hue/internal/color"
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
// 50% it is the full swatch halved. A color from color_from_hex shows with the
// largest channel of its hex value, also outside sRGB.
func TestBrightnessFromV(t *testing.T) {
	for _, hex := range []string{"#ff8800", "#7f4400", "#0000ff", "#00ff00", "#123456", "#010000", "#ffffff"} {
		xy, brightness, err := huecolor.HexToXYBrightness(hex)
		if err != nil {
			t.Fatal(err)
		}
		s := sample(map[string]any{"color_xy": map[string]any{"x": xy.X, "y": xy.Y}, "brightness": brightness}, true)
		n, _ := strconv.ParseUint(hex[1:], 16, 32)
		if got, want := max(s.R, s.G, s.B), int(max(n>>16, n>>8&255, n&255)); got != want {
			t.Errorf("%s at %g%%: largest channel %d, want %d", hex, brightness, got, want)
		}
	}
	// xy 0.17, 0.7 is a green outside sRGB, which needs a red below zero.
	green := func(brightness float64) (Sample, [3]float64) {
		s := sample(map[string]any{"color_xy": map[string]any{"x": 0.17, "y": 0.7}, "brightness": brightness}, true)
		var c [3]float64
		if _, err := fmt.Sscanf(s.CSS, "color(srgb %f %f %f)", &c[0], &c[1], &c[2]); err != nil {
			t.Fatal(err)
		}
		return s, c
	}
	s, full := green(100)
	if full[0] >= 0 || s.R != 0 || s.G != 255 {
		t.Fatalf("got %v and rgb(%d %d %d)", full, s.R, s.G, s.B)
	}
	if _, half := green(50); math.Abs(half[0]-full[0]/2) > 1e-6 || math.Abs(half[1]-full[1]/2) > 1e-6 || math.Abs(half[2]-full[2]/2) > 1e-6 {
		t.Errorf("at 50%%: %v, at 100%%: %v", half, full)
	}
}
