package color

import (
	"fmt"
	"math"
	"strconv"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

// HexToXYBrightness and XYToHex convert with the Philips Wide RGB D65 formula:
// sRGB gamma and the Wide RGB D65 matrix. Unlike sRGB, it reaches the full gamut
// of Hue lights. hue-tf show prints XYToHex, which HexToXYBrightness reads back
// to about the same xy. XYToHex prints #000000 for y <= 0, which has no color.
//
// HexToXYBrightness reads #rrggbb or #rgb. Brightness is the largest channel as a
// percentage, so that #0000ff is as bright as the light can show blue. Both are
// rounded as the provider sends them.
func HexToXYBrightness(s string) (hue.XY, float64, error) {
	hex := s
	if len(hex) == 4 && hex[0] == '#' {
		hex = string([]byte{'#', hex[1], hex[1], hex[2], hex[2], hex[3], hex[3]})
	}
	if len(hex) != 7 || hex[0] != '#' {
		return hue.XY{}, 0, fmt.Errorf("color must be #rrggbb or #rgb, got %q", s)
	}
	n, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return hue.XY{}, 0, fmt.Errorf("color must be #rrggbb or #rgb, got %q", s)
	}
	r8, g8, b8 := n>>16, n>>8&255, n&255
	if r8|g8|b8 == 0 {
		return hue.XY{}, 0, fmt.Errorf("%s is black, which has no color; turn the light off or lower its brightness instead", s)
	}
	r, g, b := DecodeSRGB(float64(r8)/255), DecodeSRGB(float64(g8)/255), DecodeSRGB(float64(b8)/255)
	x := r*0.664511 + g*0.154324 + b*0.162028
	y := r*0.283881 + g*0.668433 + b*0.047685
	z := r*0.000088 + g*0.072310 + b*0.986039
	brightness := float64(max(r8, g8, b8)) / 255 * 100
	sum := x + y + z
	return Round(hue.XY{X: x / sum, Y: y / sum}), RoundBrightness(brightness), nil
}

// XYToHex shows xy as hex at full brightness.
func XYToHex(p hue.XY) string {
	if p.Y <= 0 {
		return "#000000"
	}
	x, y, z := p.X/p.Y, 1.0, (1-p.X-p.Y)/p.Y
	r, g, b := x*1.656492-y*0.354851-z*0.255038, -x*0.707196+y*1.655397+z*0.036152, x*0.051713-y*0.121364+z*1.011530
	r, g, b = math.Max(0, r), math.Max(0, g), math.Max(0, b)
	scale := math.Max(1, math.Max(r, math.Max(g, b)))
	channel := func(v float64) int { return int(math.Round(math.Min(1, math.Max(0, EncodeSRGB(v/scale))) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", channel(r), channel(g), channel(b))
}
