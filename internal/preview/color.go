package preview

import (
	"fmt"
	"math"
	"strings"
)

func number(v any) (float64, bool) {
	n, ok := v.(float64)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}
func sample(v any, exists bool) Sample {
	s := Sample{Exists: exists}
	if !exists {
		s.Text = "—"
		return s
	}
	m, ok := v.(map[string]any)
	if !ok {
		s.Text = clean(fmt.Sprint(v))
		s.Notice = s.Text
		return s
	}
	s.Brightness = "unspecified"
	if n, ok := number(m["brightness"]); ok {
		s.Brightness = fmt.Sprintf("%g%%", n)
	} else if m["brightness"] != nil {
		s.Brightness = field(m, "brightness")
	}
	s.On = "unspecified"
	if b, ok := m["on"].(bool); ok {
		if b {
			s.On = "on"
		} else {
			s.On = "off"
		}
	} else if m["on"] != nil {
		s.On = field(m, "on")
	}
	var x, y float64
	has := false
	if xy, ok := m["color_xy"].(map[string]any); ok {
		x, xok := number(xy["x"])
		y, yok := number(xy["y"])
		if xok && yok && x >= 0 && y > 0 && x+y <= 1+1e-9 {
			s.Color = fmt.Sprintf("xy %.6g, %.6g", x, y)
			setColor(&s, x, y, m)
			has = true
		} else {
			s.Color = "xy unavailable"
			s.Notice = "Unknown or invalid color"
		}
	}
	if !has && m["color_xy"] == nil {
		k, ok := number(m["kelvin"])
		if mirek, mok := number(m["mirek"]); mok && mirek > 0 {
			k = 1e6 / mirek
			ok = true
		}
		if ok && k > 0 {
			s.Color = fmt.Sprintf("%.0f K", k)
			x, y = temperatureXY(k)
			setColor(&s, x, y, m)
			has = true
		} else if m["mirek"] != nil || m["kelvin"] != nil {
			s.Color = "temperature unavailable"
			s.Notice = "Unknown or sensitive temperature"
		} else {
			s.Color = "brightness only"
			setColor(&s, 0.3127, 0.3290, m)
		}
	} else if !has && s.Color == "" {
		s.Color = field(m, "color_xy")
		s.Notice = "Unknown or sensitive color"
	}
	for _, k := range []string{"gradient", "effects"} {
		if m[k] != nil {
			s.Extra += k + " present; "
		}
	}
	s.Extra = strings.TrimSuffix(s.Extra, "; ")
	if s.Extra != "" {
		s.Notice = strings.TrimSpace(s.Notice + " Dynamic/gradient appearance is not simulated.")
	}
	s.Text = fmt.Sprintf("%s · %s · %s", s.Color, s.Brightness, s.On)
	return s
}

// Color is a normalized D65 swatch. DimCSS scales linear luminance by the
// dimmer percentage as a visual convention, not calibrated lamp luminance.
func setColor(s *Sample, x, y float64, m map[string]any) {
	X, Y, Z := x/y, 1.0, (1-x-y)/y
	r := 3.2404542*X - 1.5371385*Y - 0.4985314*Z
	g := -0.969266*X + 1.8760108*Y + 0.041556*Z
	b := 0.0556434*X - 0.2040259*Y + 1.0572252*Z
	scale := math.Max(1, math.Max(r, math.Max(g, b)))
	X /= scale
	Y /= scale
	Z /= scale
	gamma := func(v float64) int {
		v = math.Max(0, math.Min(1, v/scale))
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return int(math.Round(v * 255))
	}
	s.R, s.G, s.B = gamma(r), gamma(g), gamma(b)
	s.HasColor = true
	s.CSS = fmt.Sprintf("color(xyz-d65 %.8f %.8f %.8f)", X, Y, Z)
	if n, ok := number(m["brightness"]); ok && n >= 0 && n <= 100 {
		s.DimCSS = fmt.Sprintf("color(xyz-d65 %.8f %.8f %.8f)", X*n/100, Y*n/100, Z*n/100)
	}
	if off, ok := m["on"].(bool); ok && !off {
		s.DimCSS = "black"
	}
}

// Approximate blackbody chromaticity for a color-temperature swatch.
func temperatureXY(k float64) (float64, float64) {
	k = math.Max(1667, math.Min(25000, k))
	var x, y float64
	if k <= 4000 {
		x = -0.2661239e9/(k*k*k) - 0.2343580e6/(k*k) + 0.8776956e3/k + 0.179910
	} else {
		x = -3.0258469e9/(k*k*k) + 2.1070379e6/(k*k) + 0.2226347e3/k + 0.240390
	}
	if k <= 2222 {
		y = -1.1063814*x*x*x - 1.3481102*x*x + 2.18555832*x - 0.20219683
	} else if k <= 4000 {
		y = -0.9549476*x*x*x - 1.37418593*x*x + 2.09137015*x - 0.16748867
	} else {
		y = 3.081758*x*x*x - 5.8733867*x*x + 3.75112997*x - 0.37001483
	}
	return x, y
}
