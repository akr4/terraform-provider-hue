package provider

import (
	"context"
	"testing"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReconcileAction(t *testing.T) {
	t.Run("import keeps only native temperature", func(t *testing.T) {
		actual := hue.Action{ColorTemperature: &hue.Temperature{Mirek: 300}}
		got := reconcileAction(emptyAction(), actual)
		if got.Mirek.ValueInt64() != 300 || !got.Kelvin.IsNull() {
			t.Fatalf("import must not populate both temperature inputs: %+v", got)
		}
		got = reconcileAction(got, actual)
		if !got.Kelvin.IsNull() {
			t.Fatal("subsequent refresh introduced kelvin")
		}
	})
	t.Run("resolves planned computed kelvin", func(t *testing.T) {
		a := emptyAction()
		a.Mirek = types.Int64Value(200)
		a.Kelvin = types.Int64Unknown()
		got := reconcileAction(a, hue.Action{ColorTemperature: &hue.Temperature{Mirek: 200}})
		if got.Kelvin.ValueInt64() != 5000 {
			t.Fatal(got)
		}
	})
	t.Run("drift replaces both representations", func(t *testing.T) {
		a := emptyAction()
		a.XY = xyValue(hue.XY{X: .6915, Y: .3083})
		a.Kelvin = types.Int64Value(2700)
		a.Mirek = types.Int64Value(370)
		a.Brightness = types.Float64Value(40)
		actual := hue.Action{Color: &hue.ActionColor{XY: hue.XY{X: .3, Y: .3}}, ColorTemperature: &hue.Temperature{Mirek: 300}, Dimming: &hue.Dimming{Brightness: 41}}
		got := reconcileAction(a, actual)
		if got.XY.Equal(a.XY) || got.Kelvin == a.Kelvin || got.Mirek == a.Mirek || got.Brightness == a.Brightness {
			t.Fatal(got)
		}
	})
	t.Run("removed values become null", func(t *testing.T) {
		a := emptyAction()
		got := reconcileAction(a, hue.Action{})
		if !got.XY.IsNull() || !got.Kelvin.IsNull() || !got.Brightness.IsNull() {
			t.Fatal(got)
		}
	})
}

// A real bridge stores scene xy and mirek as sent, even outside the light's
// gamut or range, and rounds brightness to two decimal places (#25, #28).
func TestReconcileStoredValues(t *testing.T) {
	for _, kelvin := range []int64{1000, 1800, 2000, 2700, 6500, 7000, 20000} {
		a := emptyAction()
		a.Kelvin = types.Int64Value(kelvin)
		a.Mirek = types.Int64Unknown()
		stored := colors.MirekForKelvin(kelvin)
		got := reconcileAction(a, hue.Action{ColorTemperature: &hue.Temperature{Mirek: stored}})
		if !got.Kelvin.Equal(a.Kelvin) || got.Mirek.ValueInt64() != stored {
			t.Errorf("kelvin %d stored as mirek %d: %+v", kelvin, stored, got)
		}
	}
	a := emptyAction()
	a.XY = xyValue(hue.XY{X: 0.72345, Y: 0.26})
	a.Mirek = types.Int64Value(1000)
	got := reconcileAction(a, hue.Action{Color: &hue.ActionColor{XY: colors.Round(hue.XY{X: 0.72345, Y: 0.26})}, ColorTemperature: &hue.Temperature{Mirek: 1000}})
	if !got.XY.Equal(a.XY) || !got.Mirek.Equal(a.Mirek) {
		t.Fatal(got)
	}
	for _, brightness := range []float64{49.80392156862745, 49.8039, 33.33333333333333, 40} {
		a := emptyAction()
		a.Brightness = types.Float64Value(brightness)
		got := reconcileAction(a, hue.Action{Dimming: &hue.Dimming{Brightness: colors.RoundBrightness(brightness)}})
		if !got.Brightness.Equal(a.Brightness) {
			t.Errorf("brightness %v: %+v", brightness, got)
		}
	}
}
func TestValidateAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*actionModel)
	}{
		{"temperature conflict", func(a *actionModel) { a.Mirek = types.Int64Value(370); a.Kelvin = types.Int64Value(2700) }},
		{"color and temperature", func(a *actionModel) { a.XY = xyValue(hue.XY{X: 0.3, Y: 0.3}); a.Mirek = types.Int64Value(370) }},
		{"color and kelvin", func(a *actionModel) { a.XY = xyValue(hue.XY{X: 0.3, Y: 0.3}); a.Kelvin = types.Int64Value(2700) }},
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
	for _, m := range []int64{50, 1000} {
		prior := emptyAction()
		prior.Kelvin = types.Int64Value(colors.MirekToKelvin(m))
		got := reconcileAction(prior, hue.Action{ColorTemperature: &hue.Temperature{Mirek: m}})
		if !got.Kelvin.Equal(prior.Kelvin) || got.Mirek.ValueInt64() != m {
			t.Fatalf("mirek %d: %+v", m, got)
		}
	}
	prior := emptyAction()
	prior.Mirek = types.Int64Value(750)
	got := reconcileAction(prior, hue.Action{ColorTemperature: &hue.Temperature{Mirek: 500}})
	if got.Mirek.Equal(prior.Mirek) {
		t.Fatal("temperature drift masked")
	}
}
