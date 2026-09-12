package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSceneRefreshCapabilityRequests(t *testing.T) {
	const count = 10
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			b := fakebridge.New()
			defer b.Close()
			r := sceneResource{client: b.Client()}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)
				b.Put("scene", id, hue.Scene{ID: id, Type: "scene", Metadata: hue.Metadata{Name: "Read"}, Group: hue.Reference{RID: "room", RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}}}}})
			}
			start := time.Now()
			for i := 0; i < count; i++ {
				if !shared {
					r.client.ResetLightCapabilities()
				}
				m := sceneModel{ID: types.StringValue(fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)), Actions: types.MapNull(actionType)}
				if err := r.refresh(context.Background(), &m); err != nil {
					t.Fatal(err)
				}
			}
			lights, scenes := 0, 0
			for _, req := range b.Requests() {
				if req.Path == "/clip/v2/resource/light" {
					lights++
				} else {
					scenes++
				}
			}
			want := count
			if shared {
				want = 1
			}
			if lights != want || scenes != count {
				t.Fatalf("lights=%d scenes=%d", lights, scenes)
			}
			t.Logf("%d scenes: %d GETs in %s", count, lights+scenes, time.Since(start).Round(time.Millisecond))
		})
	}
}
