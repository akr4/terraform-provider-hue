package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
)

func TestSwitchCLI(t *testing.T) {
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	b := fakebridge.New()
	defer b.Close()
	const deviceID = "44444444-4444-4444-8444-444444444444"
	const behaviorID = "55555555-5555-4555-8555-555555555555"
	device := hue.Device{ID: deviceID, Type: "device", Metadata: hue.Metadata{Name: "Switch\nname"}, Services: []hue.Reference{{RID: "button", RType: "button"}}}
	device.ProductData.ModelID = "RWL022"
	b.Put("device", deviceID, device)
	deps := dependencies{terraform: mockPullTerraform, newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }}
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"ls", "switch"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no v2 assignment") {
		t.Fatal(out.String())
	}
	b.Put("behavior_instance", behaviorID, hue.BehaviorInstance{ID: behaviorID, Type: "behavior_instance", Status: "running", Configuration: json.RawMessage(`{"device":{"rid":"` + deviceID + `","rtype":"device"}}`)})
	out.Reset()
	if err := runWith(context.Background(), []string{"ls", "switch", "--json"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		DeviceID   string `json:"device_id"`
		BehaviorID string `json:"behavior_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BehaviorID != behaviorID || rows[0].DeviceID != deviceID {
		t.Fatal(out.String())
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge mutation")
		}
	}
}
func TestPullBehaviorCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	b := fakebridge.New()
	defer b.Close()
	const id = "55555555-5555-4555-8555-555555555555"
	behavior := hue.BehaviorInstance{ID: id, Type: "behavior_instance", ScriptID: fakebridge.DeviceID, Metadata: hue.Metadata{Name: "Switch"}, Enabled: true, Configuration: json.RawMessage(`{"buttons":{"one":{"scene":"old"}},"keep":false}`)}
	b.Put("behavior_instance", id, behavior)
	imported := false
	deps := dependencies{terraform: mockPullTerraform, newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) {
		if !imported {
			return []byte(`{"resources":[]}`), nil
		}
		baseline := pull.Baseline{ID: id, Name: "Switch", Enabled: true, ScriptID: behavior.ScriptID, Configuration: string(behavior.Configuration)}
		state := map[string]any{"resources": []any{map[string]any{"mode": "managed", "type": "hue_behavior_instance", "name": "test", "module": "module.bedroom", "provider": `provider["registry.terraform.io/akr4/hue"]`, "instances": []any{map[string]any{"attributes": baseline}}}}}
		return json.Marshal(state)
	}}
	if err := os.Mkdir("bedroom", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.tf", []byte("module \"bedroom\" { source = \"./bedroom\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	addr := "module.bedroom.hue_behavior_instance.test"
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"pull", "--new"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "behavior_instance:") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := runWith(context.Background(), []string{"pull", "--new", id, addr}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "to = "+addr) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat("bedroom/behavior_instance_test.tf"); !os.IsNotExist(err) {
		t.Fatal("preview wrote")
	}
	if err := runWith(context.Background(), []string{"pull", "--new", id, addr, "--write"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	// Simulate resource configuration produced separately by Terraform.
	raw, e := json.Marshal(behavior)
	if e != nil {
		t.Fatal(e)
	}
	src, e := pull.NewBehavior(raw, "test")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("bedroom/behavior_instance_test.tf", src, 0600); e != nil {
		t.Fatal(e)
	}
	imported = true
	out.Reset()
	if err := runWith(context.Background(), []string{"pull", "--new"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), id) {
		t.Fatal("managed behavior listed as new")
	}
	if err := runWith(context.Background(), []string{"pull", "--new", id, addr}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	remote := behavior
	remote.Enabled = false
	b.Put("behavior_instance", id, remote)
	out.Reset()
	if err := runWith(context.Background(), []string{"pull", addr, "--write"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "true -> false") {
		t.Fatal(out.String())
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge mutation")
		}
	}
}
