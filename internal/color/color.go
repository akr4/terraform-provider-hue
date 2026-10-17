// Package color provides temperature conversions, the rounding that the bridge
// applies to scene values, and display colors. XY is independent of brightness;
// hex reconstructed from XY is for display only.
package color

import (
	"fmt"
	"math"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

// Hue API v2 bounds; individual lights can advertise a narrower mirek_schema.
const MinMirek int64 = 50
const MaxMirek int64 = 1000

func KelvinToMirek(k int64) int64 {
	if k <= 0 {
		return 0
	}
	return int64(math.Round(1e6 / float64(k)))
}
func MirekToKelvin(m int64) int64 {
	if m <= 0 {
		return 0
	}
	return int64(math.Round(1e6 / float64(m)))
}

// MirekForKelvin is the mirek that the provider sends for a kelvin: converted and
// clamped to the API range.
func MirekForKelvin(k int64) int64 { return ClampMirek(KelvinToMirek(k), MinMirek, MaxMirek) }
func ClampMirek(m, min, max int64) int64 {
	if min <= 0 || max < min {
		min, max = MinMirek, MaxMirek
	}
	if m < min {
		return min
	}
	if m > max {
		return max
	}
	return m
}
func gamma(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}
func XYToHex(p hue.XY) string {
	if p.Y <= 0 {
		return "#000000"
	}
	x, y, z := p.X/p.Y, 1.0, (1-p.X-p.Y)/p.Y
	r, g, b := x*1.656492-y*0.354851-z*0.255038, -x*0.707196+y*1.655397+z*0.036152, x*0.051713-y*0.121364+z*1.011530
	r, g, b = math.Max(0, r), math.Max(0, g), math.Max(0, b)
	scale := math.Max(1, math.Max(r, math.Max(g, b)))
	channel := func(v float64) int { return int(math.Round(math.Min(1, math.Max(0, gamma(v/scale))) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", channel(r), channel(g), channel(b))
}
func Round(p hue.XY) hue.XY {
	return hue.XY{X: math.Round(p.X*1e4) / 1e4, Y: math.Round(p.Y*1e4) / 1e4}
}

// The bridge stores scene xy and mirek as sent, even outside a light's gamut or
// range, and rounds brightness to two decimal places. Values are equal when the
// bridge stored what the provider sends for the configured value.
func RoundBrightness(v float64) float64 { return math.Round(v*100) / 100 }
func EqualXY(config, actual hue.XY) bool {
	p := Round(config)
	return math.Abs(p.X-actual.X) <= 0.001+1e-12 && math.Abs(p.Y-actual.Y) <= 0.001+1e-12
}
func EqualMirek(sent, actual int64) bool { return math.Abs(float64(sent-actual)) <= 1 }
func EqualBrightness(config, actual float64) bool {
	return math.Abs(RoundBrightness(config)-RoundBrightness(actual)) <= 1e-9
}
