// Package color provides temperature conversions, gamut clipping and display colors. XY is independent
// of brightness; hex reconstructed from XY is for display only.
package color

import (
	"fmt"
	"math"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

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
func ClampMirek(m, min, max int64) int64 {
	if min <= 0 || max < min {
		min, max = 153, 500
	}
	if m < min {
		return min
	}
	if m > max {
		return max
	}
	return m
}
func EqualMirek(a, b, min, max int64) bool { return math.Abs(float64(ClampMirek(a, min, max)-b)) <= 1 }
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
func Gamut(kind string) *hue.Gamut {
	switch kind {
	case "A":
		return &hue.Gamut{Red: hue.XY{X: 0.704, Y: 0.296}, Green: hue.XY{X: 0.2151, Y: 0.7106}, Blue: hue.XY{X: 0.138, Y: 0.08}}
	case "B":
		return &hue.Gamut{Red: hue.XY{X: 0.675, Y: 0.322}, Green: hue.XY{X: 0.409, Y: 0.518}, Blue: hue.XY{X: 0.167, Y: 0.04}}
	case "C":
		return &hue.Gamut{Red: hue.XY{X: 0.6915, Y: 0.3083}, Green: hue.XY{X: 0.17, Y: 0.7}, Blue: hue.XY{X: 0.1532, Y: 0.0475}}
	}
	return nil
}
func cross(a, b, c hue.XY) float64 { return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X) }
func distance(a, b hue.XY) float64 { return (a.X-b.X)*(a.X-b.X) + (a.Y-b.Y)*(a.Y-b.Y) }
func nearest(p, a, b hue.XY) hue.XY {
	d := distance(a, b)
	if d == 0 {
		return a
	}
	t := math.Max(0, math.Min(1, ((p.X-a.X)*(b.X-a.X)+(p.Y-a.Y)*(b.Y-a.Y))/d))
	return hue.XY{X: a.X + t*(b.X-a.X), Y: a.Y + t*(b.Y-a.Y)}
}
func Clip(p hue.XY, g *hue.Gamut) hue.XY {
	if g == nil {
		return p
	}
	a, b, c := cross(g.Red, g.Green, p), cross(g.Green, g.Blue, p), cross(g.Blue, g.Red, p)
	if (a >= 0 && b >= 0 && c >= 0) || (a <= 0 && b <= 0 && c <= 0) {
		return p
	}
	result := nearest(p, g.Red, g.Green)
	for _, candidate := range []hue.XY{nearest(p, g.Green, g.Blue), nearest(p, g.Blue, g.Red)} {
		if distance(p, candidate) < distance(p, result) {
			result = candidate
		}
	}
	return result
}
func Round(p hue.XY) hue.XY {
	return hue.XY{X: math.Round(p.X*1e4) / 1e4, Y: math.Round(p.Y*1e4) / 1e4}
}
func EqualXY(config, actual hue.XY, g *hue.Gamut) bool {
	p := Round(Clip(config, g))
	return math.Abs(p.X-actual.X) <= 0.001+1e-12 && math.Abs(p.Y-actual.Y) <= 0.001+1e-12
}
