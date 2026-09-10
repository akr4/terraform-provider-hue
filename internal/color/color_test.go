package color

import (
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"math"
	"testing"
)

func TestHexConversion(t *testing.T) {
	for _, tc := range []struct {
		hex string
		xy  hue.XY
	}{{"#ff0000", hue.XY{X: 0.7006, Y: 0.2993}}, {"#00ff00", hue.XY{X: 0.1724, Y: 0.7468}}, {"#0000ff", hue.XY{X: 0.1355, Y: 0.0399}}, {"#000000", hue.XY{}}} {
		t.Run(tc.hex, func(t *testing.T) {
			p, err := HexToXY(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(p.X-tc.xy.X) > 0.0001 || math.Abs(p.Y-tc.xy.Y) > 0.0001 {
				t.Fatalf("got %+v", p)
			}
			if got := XYToHex(p); got != tc.hex {
				t.Fatalf("inverse = %s", got)
			}
		})
	}
	for _, hex := range []string{"red", "#fff", "#gg0000", "ff0000"} {
		if _, err := HexToXY(hex); err == nil {
			t.Errorf("accepted %s", hex)
		}
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
