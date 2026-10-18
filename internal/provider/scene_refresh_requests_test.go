package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The bridge stores scene values as sent, so a refresh compares them without
// the lights' gamut or temperature range and reads only the scene.
func TestSceneRefreshReadsOnlyTheScene(t *testing.T) {
	const count = 10
	b := fakebridge.New()
	defer b.Close()
	r := sceneResource{client: b.Client()}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)
		b.Put("scene", id, hue.Scene{ID: id, Type: "scene", Metadata: hue.Metadata{Name: "Read"}, Group: hue.Reference{RID: "room", RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 40}, Color: &hue.ActionColor{XY: hue.XY{X: 0.72, Y: 0.26}}, ColorTemperature: &hue.Temperature{Mirek: 1000}}}}})
	}
	for i := 0; i < count; i++ {
		prior := emptyAction()
		prior.XY = xyValue(hue.XY{X: 0.72, Y: 0.26})
		prior.Mirek = types.Int64Value(1000)
		prior.Brightness = types.Float64Value(40)
		actions, d := types.MapValueFrom(context.Background(), actionType, map[string]actionModel{fakebridge.LightID: prior})
		if d.HasError() {
			t.Fatal(d)
		}
		m := sceneModel{ID: types.StringValue(fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)), Actions: actions}
		if err := r.refresh(context.Background(), &m); err != nil {
			t.Fatal(err)
		}
	}
	for _, req := range b.Requests() {
		if req.Method != "GET" || req.Path == "/clip/v2/resource/light" {
			t.Errorf("unexpected request %s %s", req.Method, req.Path)
		}
	}
	if n := len(b.Requests()); n != count {
		t.Fatalf("%d refreshes made %d requests", count, n)
	}
}
