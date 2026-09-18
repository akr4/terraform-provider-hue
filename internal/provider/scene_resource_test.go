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
		a.XY = xyValue(hue.XY{X: .6915, Y: .3083})
		a.Kelvin = types.Int64Value(2700)
		a.Mirek = types.Int64Value(370)
		actual := hue.Action{Color: &hue.ActionColor{XY: hue.XY{X: .3, Y: .3}}, ColorTemperature: &hue.Temperature{Mirek: 300}}
		got := reconcileAction(a, actual, light)
		if got.XY.Equal(a.XY) || got.Kelvin == a.Kelvin || got.Mirek == a.Mirek {
			t.Fatal(got)
		}
	})
	t.Run("removed values become null", func(t *testing.T) {
		a := emptyAction()
		got := reconcileAction(a, hue.Action{}, light)
		if !got.XY.IsNull() || !got.Kelvin.IsNull() {
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
	prior.XY = xyValue(hue.XY{X: 0.3, Y: 0.4})
	prior.Kelvin = types.Int64Value(2700)
	prior.Mirek = types.Int64Value(370)
	config := emptyAction()
	config.Kelvin = prior.Kelvin
	planned := config
	planned.Mirek = types.Int64Unknown()
	got := planAction(config, planned, prior)
	if got.Mirek != prior.Mirek {
		t.Fatal("unchanged counterparts not preserved")
	}
	config.Kelvin = types.Int64Value(3000)
	got = planAction(config, config, prior)
	if !got.Mirek.IsUnknown() {
		t.Fatal("stale counterparts retained")
	}
	config = emptyAction()
	got = planAction(config, prior, prior)
	if !got.XY.IsNull() || !got.Mirek.IsNull() || !got.Kelvin.IsNull() {
		t.Fatal("absent pair was not removed")
	}
}

func TestExtendedTemperatureValidation(t *testing.T) {
	for _, m := range []int64{49, 50, 100, 153, 500, 750, 1000, 1001} {
		a := emptyAction()
		a.Mirek = types.Int64Value(m)
		errs := validateAction(a)
		if (len(errs) == 0) != (m >= 50 && m <= 1000) {
			t.Fatalf("mirek %d: %v", m, errs)
		}
	}
}

func TestExtendedTemperatureReconcile(t *testing.T) {
	for _, bounds := range []hue.MirekSchema{{Min: 50, Max: 1000}, {}, {Min: 1000, Max: 50}} {
		for _, m := range []int64{50, 1000} {
			prior := emptyAction()
			prior.Kelvin = types.Int64Value(colors.MirekToKelvin(m))
			actual := hue.Action{ColorTemperature: &hue.Temperature{Mirek: m}}
			light := hue.Light{ColorTemperature: &hue.ColorTemperature{MirekSchema: bounds}}
			got := reconcileAction(prior, actual, light)
			if !got.Kelvin.Equal(prior.Kelvin) || got.Mirek.ValueInt64() != m {
				t.Fatalf("%+v: %+v", bounds, got)
			}
		}
	}
	prior := emptyAction()
	prior.Mirek = types.Int64Value(750)
	got := reconcileAction(prior, hue.Action{ColorTemperature: &hue.Temperature{Mirek: 500}}, hue.Light{})
	if got.Mirek.Equal(prior.Mirek) {
		t.Fatal("missing capability data masked temperature drift")
	}
}
