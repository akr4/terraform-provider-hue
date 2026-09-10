package provider

import (
	"context"
	"testing"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReconcileAction(t *testing.T) {
	light := hue.Light{Color: &hue.Color{GamutType: "C"}, ColorTemperature: &hue.ColorTemperature{MirekSchema: hue.MirekSchema{Min: 200, Max: 450}}}
	t.Run("preserves clipped hex", func(t *testing.T) {
		a := emptyAction()
		a.Hex = types.StringValue("#FF0000")
		a.XY = types.ObjectUnknown(xyTypes)
		p, _ := colors.HexToXY(a.Hex.ValueString())
		actual := hue.Action{Color: &hue.ActionColor{XY: colors.Round(colors.Clip(p, colors.Gamut("C")))}}
		got := reconcileAction(a, actual, light)
		if got.Hex != a.Hex {
			t.Fatal(got.Hex)
		}
		again := reconcileAction(got, actual, light)
		if !again.XY.Equal(got.XY) || again.Hex != got.Hex {
			t.Fatal("unstable refresh")
		}
	})
	t.Run("preserves clipped kelvin", func(t *testing.T) {
		a := emptyAction()
		a.Kelvin = types.Int64Value(1000)
		a.Mirek = types.Int64Unknown()
		actual := hue.Action{ColorTemperature: &hue.Temperature{Mirek: 450}}
		got := reconcileAction(a, actual, light)
		if got.Kelvin != a.Kelvin || got.Mirek.ValueInt64() != 450 {
			t.Fatal(got)
		}
	})
	t.Run("preserves direct xy and mirek", func(t *testing.T) {
		a := emptyAction()
		a.XY = xyValue(hue.XY{X: .9, Y: .1})
		a.Mirek = types.Int64Value(500)
		actual := hue.Action{Color: &hue.ActionColor{XY: colors.Round(colors.Clip(hue.XY{X: .9, Y: .1}, colors.Gamut("C")))}, ColorTemperature: &hue.Temperature{Mirek: 450}}
		got := reconcileAction(a, actual, light)
		if !got.XY.Equal(a.XY) || got.Mirek != a.Mirek {
			t.Fatal(got)
		}
	})
	t.Run("drift replaces both representations", func(t *testing.T) {
		a := emptyAction()
		a.Hex = types.StringValue("#ff0000")
		a.XY = xyValue(hue.XY{X: .6915, Y: .3083})
		a.Kelvin = types.Int64Value(2700)
		a.Mirek = types.Int64Value(370)
		actual := hue.Action{Color: &hue.ActionColor{XY: hue.XY{X: .3, Y: .3}}, ColorTemperature: &hue.Temperature{Mirek: 300}}
		got := reconcileAction(a, actual, light)
		if got.Hex == a.Hex || got.XY.Equal(a.XY) || got.Kelvin == a.Kelvin || got.Mirek == a.Mirek {
			t.Fatal(got)
		}
	})
	t.Run("removed values become null", func(t *testing.T) {
		a := emptyAction()
		a.Hex = types.StringValue("#ff0000")
		got := reconcileAction(a, hue.Action{}, light)
		if !got.Hex.IsNull() || !got.XY.IsNull() || !got.Kelvin.IsNull() {
			t.Fatal(got)
		}
	})
}
func TestValidateAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*actionModel)
	}{
		{"temperature conflict", func(a *actionModel) { a.Mirek = types.Int64Value(370); a.Kelvin = types.Int64Value(2700) }},
		{"color conflict", func(a *actionModel) { a.Hex = types.StringValue("#ff0000"); a.XY = xyValue(hue.XY{X: .3, Y: .3}) }},
		{"bad hex", func(a *actionModel) { a.Hex = types.StringValue("red") }},
		{"bad brightness", func(a *actionModel) { a.Brightness = types.Float64Value(101) }},
		{"bad kelvin", func(a *actionModel) { a.Kelvin = types.Int64Value(0) }},
		{"bad xy", func(a *actionModel) { a.XY = xyValue(hue.XY{X: .8, Y: .8}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := emptyAction()
			tc.change(&a)
			if len(validateAction(a)) == 0 {
				t.Fatal("invalid action accepted")
			}
		})
	}
	if errs := validateAction(emptyAction()); len(errs) > 0 {
		t.Fatal(errs)
	}
}
func TestActionsUnknown(t *testing.T) {
	a := emptyAction()
	a.XY = types.ObjectUnknown(xyTypes)
	a.Mirek = types.Int64Unknown()
	m, d := types.MapValueFrom(context.Background(), actionType, map[string]actionModel{"light": a})
	if d.HasError() {
		t.Fatal(d)
	}
	decoded, d := actionsFrom(context.Background(), m)
	if d.HasError() || !decoded["light"].XY.IsUnknown() {
		t.Fatalf("%v %v", decoded, d)
	}
}

func TestPlanAction(t *testing.T) {
	prior := emptyAction()
	prior.Hex = types.StringValue("#FF0000")
	prior.XY = xyValue(hue.XY{X: .6915, Y: .3083})
	prior.Kelvin = types.Int64Value(2700)
	prior.Mirek = types.Int64Value(370)
	config := emptyAction()
	config.Hex = prior.Hex
	config.Kelvin = prior.Kelvin
	planned := config
	planned.XY = types.ObjectUnknown(xyTypes)
	planned.Mirek = types.Int64Unknown()
	got := planAction(config, planned, prior)
	if !got.XY.Equal(prior.XY) || got.Mirek != prior.Mirek {
		t.Fatal("unchanged counterparts not preserved")
	}
	config.Hex = types.StringValue("#00ff00")
	config.Kelvin = types.Int64Value(3000)
	got = planAction(config, config, prior)
	if !got.XY.IsUnknown() || !got.Mirek.IsUnknown() {
		t.Fatal("stale counterparts retained")
	}
	config = emptyAction()
	got = planAction(config, prior, prior)
	if !got.XY.IsNull() || !got.Hex.IsNull() || !got.Mirek.IsNull() || !got.Kelvin.IsNull() {
		t.Fatal("absent pair was not removed")
	}
	config = emptyAction()
	config.Hex = types.StringUnknown()
	got = planAction(config, config, prior)
	if !got.XY.IsUnknown() {
		t.Fatal("unknown primary reused counterpart")
	}
}
