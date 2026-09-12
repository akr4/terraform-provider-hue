package color

import (
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"testing"
)

func TestXYDisplay(t *testing.T) {
	for _, xy := range []hue.XY{{X: 0.3, Y: 0.4}, {X: 0.7, Y: 0.3}, {}} {
		got := XYToHex(xy)
		if len(got) != 7 || got[0] != '#' {
			t.Fatal(got)
		}
	}
	if got := XYToHex(hue.XY{}); got != "#000000" {
		t.Fatal(got)
	}
}
func TestGamutClipping(t *testing.T) {
	for _, kind := range []string{"A", "B", "C"} {
		t.Run(kind, func(t *testing.T) {
			g := Gamut(kind)
			inside := hue.XY{X: (g.Red.X + g.Green.X + g.Blue.X) / 3, Y: (g.Red.Y + g.Green.Y + g.Blue.Y) / 3}
			if Clip(inside, g) != inside {
				t.Fatal("inside moved")
			}
			p := Clip(hue.XY{X: 1, Y: 0}, g)
			if !EqualXY(hue.XY{X: 1, Y: 0}, p, g) {
				t.Fatal("clipping not equivalent")
			}
			if distance(Clip(p, g), p) > 1e-12 {
				t.Fatal("clip not idempotent")
			}
		})
	}
	if Gamut("other") != nil {
		t.Fatal("unknown gamut")
	}
}
func TestSemanticTolerance(t *testing.T) {
	if !EqualXY(hue.XY{X: .3, Y: .3}, hue.XY{X: .301, Y: .299}, nil) {
		t.Fatal("boundary should match")
	}
	if EqualXY(hue.XY{X: .3, Y: .3}, hue.XY{X: .3011, Y: .3}, nil) {
		t.Fatal("drift should differ")
	}
	for _, tc := range []struct{ k, m int64 }{{2700, 370}, {2000, 500}, {6500, 154}} {
		if KelvinToMirek(tc.k) != tc.m {
			t.Errorf("kelvin %d", tc.k)
		}
	}
	if MirekToKelvin(370) != 2703 {
		t.Fatal("inverse rounding")
	}
	if !EqualMirek(500, 450, 200, 450) || !EqualMirek(370, 371, 153, 500) || EqualMirek(370, 372, 153, 500) {
		t.Fatal("mirek tolerance/clipping")
	}
}
