package provider

import (
	"context"
	"testing"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReconcileAction(t *testing.T) {
	t.Run("import reads mirek", func(t *testing.T) {
		actual := hue.Action{ColorTemperature: &hue.Temperature{Mirek: 300}}
		got := reconcileAction(emptyAction(), actual)
		if got.Mirek.ValueInt64() != 300 {
			t.Fatalf("import read %+v", got)
		}
		if again := reconcileAction(got, actual); !again.Mirek.Equal(got.Mirek) {
			t.Fatalf("refresh read %+v", again)
		}
	})
	t.Run("drift replaces the written values", func(t *testing.T) {
		a := emptyAction()
		a.XY = xyValue(hue.XY{X: .6915, Y: .3083})
		a.Mirek = types.Int64Value(370)
		a.Brightness = types.Float64Value(40)
		actual := hue.Action{Color: &hue.ActionColor{XY: hue.XY{X: .3, Y: .3}}, ColorTemperature: &hue.Temperature{Mirek: 300}, Dimming: &hue.Dimming{Brightness: 41}}
		got := reconcileAction(a, actual)
		if got.XY.Equal(a.XY) || got.Mirek == a.Mirek || got.Brightness == a.Brightness {
			t.Fatal(got)
		}
	})
	t.Run("removed values become null", func(t *testing.T) {
		a := emptyAction()
		got := reconcileAction(a, hue.Action{})
		if !got.XY.IsNull() || !got.Mirek.IsNull() || !got.Brightness.IsNull() {
			t.Fatal(got)
		}
	})
}

// A real bridge stores scene xy and mirek as sent, even outside the light's
// gamut or range, and rounds brightness to two decimal places (#25, #28).
func TestReconcileStoredValues(t *testing.T) {
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
		{"color and temperature", func(a *actionModel) { a.XY = xyValue(hue.XY{X: 0.3, Y: 0.3}); a.Mirek = types.Int64Value(370) }},
		{"bad brightness", func(a *actionModel) { a.Brightness = types.Float64Value(101) }},
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
	planned := emptyAction()
	planned.XY = types.ObjectUnknown(xyTypes)
	got := planAction(emptyAction(), planned, prior)
	if !got.XY.IsNull() {
		t.Fatal("removed color was kept")
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
		prior.Mirek = types.Int64Value(m)
		got := reconcileAction(prior, hue.Action{ColorTemperature: &hue.Temperature{Mirek: m}})
		if !got.Mirek.Equal(prior.Mirek) {
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
