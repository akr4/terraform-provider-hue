package hue_test

import (
	"context"
	"sync"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func inventoryRequests(b *fakebridge.Bridge) int {
	n := 0
	for _, r := range b.Requests() {
		if r.Method == "GET" && r.Path == "/clip/v2/resource/light" {
			n++
		}
	}
	return n
}
func TestLightCapabilities(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	c := b.Client()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			caps, err := c.LightCapabilities(ctx, []string{fakebridge.LightID})
			if err != nil {
				t.Error(err)
				return
			}
			if caps[fakebridge.LightID].Color == nil {
				t.Error("missing color capability")
			}
		}()
	}
	wg.Wait()
	if n := inventoryRequests(b); n != 1 {
		t.Fatalf("20 scene lookups made %d inventory requests, want 1", n)
	}
	caps, _ := c.LightCapabilities(ctx, []string{fakebridge.LightID})
	if caps[fakebridge.LightID].Color.XY != (hue.XY{}) || caps[fakebridge.LightID].ColorTemperature.Mirek != nil {
		t.Fatal("cached live light state")
	}
	caps[fakebridge.LightID].Color.GamutType = "changed"
	again, _ := c.LightCapabilities(ctx, []string{fakebridge.LightID})
	if again[fakebridge.LightID].Color.GamutType == "changed" {
		t.Fatal("caller mutated cache")
	}
	c.ResetLightCapabilities()
	if _, err := c.LightCapabilities(ctx, []string{fakebridge.LightID}); err != nil {
		t.Fatal(err)
	}
	if n := inventoryRequests(b); n != 2 {
		t.Fatalf("reset did not reload: %d", n)
	}
	// No write changes a light's gamut or temperature range, so writes keep the snapshot.
	for i := 0; i < 3; i++ {
		id, err := c.Create(ctx, "room", hue.Group{Metadata: hue.Metadata{Name: "New"}, Children: []hue.Reference{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Update(ctx, "room", id, map[string]any{"metadata": map[string]string{"name": "Renamed"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.LightCapabilities(ctx, []string{fakebridge.LightID}); err != nil {
			t.Fatal(err)
		}
	}
	if n := inventoryRequests(b); n != 2 {
		t.Fatalf("writes reloaded the inventory: %d requests", n)
	}
	const added = "11111111-2222-4333-8444-555555555555"
	b.Put("light", added, hue.Light{ID: added, Type: "light"})
	if caps, err := c.LightCapabilities(ctx, []string{added}); err != nil || caps[added].ID != added {
		t.Fatalf("new light not refreshed: %v", err)
	}
	if n := inventoryRequests(b); n != 3 {
		t.Fatalf("missing light did not reload: %d", n)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.LightCapabilities(canceled, []string{fakebridge.LightID}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
