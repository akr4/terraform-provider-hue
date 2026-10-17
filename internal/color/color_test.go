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
func TestSemanticTolerance(t *testing.T) {
	if !EqualXY(hue.XY{X: .3, Y: .3}, hue.XY{X: .301, Y: .299}) {
		t.Fatal("boundary should match")
	}
	if EqualXY(hue.XY{X: .3, Y: .3}, hue.XY{X: .3011, Y: .3}) {
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
	if !EqualMirek(370, 371) || EqualMirek(370, 372) || EqualMirek(500, 450) {
		t.Fatal("mirek tolerance")
	}
}

// The bridge stores xy as sent, even outside a light's gamut, and rounds
// brightness to two decimal places.
func TestStoredValues(t *testing.T) {
	if !EqualXY(hue.XY{X: 0.72345, Y: 0.26}, hue.XY{X: 0.7235, Y: 0.26}) {
		t.Fatal("xy outside the gamut is stored as sent")
	}
	for _, tc := range []struct{ config, stored float64 }{{49.80392156862745, 49.8}, {49.8039, 49.8}, {33.33333333333333, 33.33}, {49.8, 49.8}, {40, 40}} {
		if got := RoundBrightness(tc.config); got != tc.stored {
			t.Errorf("RoundBrightness(%v) = %v", tc.config, got)
		}
		if !EqualBrightness(tc.config, tc.stored) {
			t.Errorf("brightness %v stored as %v", tc.config, tc.stored)
		}
	}
	if EqualBrightness(33.33, 33.34) {
		t.Fatal("brightness drift should differ")
	}
}
