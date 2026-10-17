package color

import (
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func TestHexToXYBrightness(t *testing.T) {
	for _, tc := range []struct {
		hex        string
		xy         hue.XY
		brightness float64
	}{
		{"#ff8800", hue.XY{X: 0.601, Y: 0.3837}, 100},
		{"#F80", hue.XY{X: 0.601, Y: 0.3837}, 100},
		{"#7f4400", hue.XY{X: 0.5926, Y: 0.3908}, 49.8},
		{"#0000ff", hue.XY{X: 0.1355, Y: 0.0399}, 100},
		{"#00ff00", hue.XY{X: 0.1724, Y: 0.7468}, 100},
		{"#ffffff", hue.XY{X: 0.3227, Y: 0.329}, 100},
	} {
		xy, brightness, err := HexToXYBrightness(tc.hex)
		if err != nil || xy != tc.xy || brightness != tc.brightness {
			t.Errorf("%s: got %v %v %v", tc.hex, xy, brightness, err)
		}
	}
	for _, hex := range []string{"", "ff8800", "#ff880", "#ff8800aa", "#gggggg", "#+f8800", "#000000", "#000"} {
		if _, _, err := HexToXYBrightness(hex); err == nil {
			t.Errorf("%q accepted", hex)
		}
	}
}

// hue-tf show prints XYToHex, which color_from_hex reads back to about the
// same xy.
func TestHexRoundTrip(t *testing.T) {
	for _, hex := range []string{"#ff8800", "#7f4400", "#3355ff", "#00ccaa", "#ffffff"} {
		xy, _, err := HexToXYBrightness(hex)
		if err != nil {
			t.Fatal(err)
		}
		back, _, err := HexToXYBrightness(XYToHex(xy))
		if err != nil || !EqualXY(back, xy) {
			t.Errorf("%s: %v became %s, %v", hex, xy, XYToHex(xy), back)
		}
	}
}
