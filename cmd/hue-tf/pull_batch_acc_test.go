package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	hueprovider "github.com/akr4/terraform-provider-hue/internal/provider"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
)

type pullTestProvider struct {
	provider.Provider
	client *hue.Client
}

func (p *pullTestProvider) Configure(_ context.Context, _ provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	resp.ResourceData = p.client
	resp.DataSourceData = p.client
}

// TestAccPullRoundTrip exercises real Terraform import and refresh-only apply;
// a test provider transports every API request to an in-memory HTTPS bridge.
func TestAccPullRoundTrip(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 for Terraform integration")
	}
	tf := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if tf == "" {
		t.Fatal("TF_ACC_TERRAFORM_PATH is required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TF_DATA_DIR", filepath.Join(root, ".terraform"))
	t.Setenv("TF_WORKSPACE", "default")
	t.Setenv("HUE_BRIDGE_HOST", "test-bridge")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	t.Setenv("TF_CLI_ARGS", "")
	t.Setenv("TF_CLI_ARGS_plan", "")
	t.Setenv("TF_CLI_ARGS_apply", "")
	b := fakebridge.New()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := make(chan *plugin.ReattachConfig)
	closed := make(chan struct{})
	go func() {
		_ = tf6server.Serve("registry.terraform.io/akr4/hue", func() tfprotov6.ProviderServer {
			return providerserver.NewProtocol6(&pullTestProvider{Provider: hueprovider.New("test")(), client: b.Client()})()
		}, tf6server.WithDebug(ctx, config, closed))
	}()
	var reattach *plugin.ReattachConfig
	select {
	case reattach = <-config:
	case <-time.After(10 * time.Second):
		t.Fatal("provider startup timeout")
	}
	data, e := json.Marshal(map[string]any{"registry.terraform.io/akr4/hue": map[string]any{"Protocol": string(reattach.Protocol), "ProtocolVersion": reattach.ProtocolVersion, "Pid": reattach.Pid, "Test": true, "Addr": map[string]string{"Network": reattach.Addr.Network(), "String": reattach.Addr.String()}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("TF_REATTACH_PROVIDERS", string(data))
	configPath := filepath.Join(root, "dev.tfrc")
	if e = os.WriteFile(configPath, []byte("provider_installation {\n dev_overrides {\n \"akr4/hue\" = \""+root+"\"\n }\n direct {}\n}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", configPath)
	if e = os.WriteFile("main.tf", []byte("terraform {\n required_providers {\n hue = { source = \"akr4/hue\" }\n }\n}\nprovider \"hue\" {}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, tf, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		if e != nil {
			t.Log(stderr.String())
		}
		return out, e
	}
	deps := dependencies{terraform: run, newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	group := hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: "Imported", Archetype: "bedroom"}, Children: []hue.Reference{}}
	b.Put("room", id, group)
	var output bytes.Buffer
	if e = pullBatch(ctx, nil, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	if _, e = os.Stat("room_Imported.tf"); !os.IsNotExist(e) {
		t.Fatal("preview created definition")
	}
	if e = pullBatch(ctx, []string{"--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	state, e := stateReader(deps)(ctx)
	if e != nil || !bytes.Contains(state, []byte(id)) {
		t.Fatalf("state: %s %v", state, e)
	}
	group.Metadata.Name = "From app"
	b.Put("room", id, group)
	if e = pullBatch(ctx, []string{id, "--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	contents, e := os.ReadFile("room_Imported.tf")
	if e != nil || !strings.Contains(string(contents), "From app") {
		t.Fatalf("%s %v", contents, e)
	}
	state, e = stateReader(deps)(ctx)
	if e != nil || !bytes.Contains(state, []byte("From app")) {
		t.Fatalf("state: %s %v", state, e)
	}
	sceneID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	scene := hue.Scene{ID: sceneID, Type: "scene", Metadata: hue.Metadata{Name: "Evening"}, Group: hue.Reference{RID: id, RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 20}, ColorTemperature: &hue.Temperature{Mirek: 350}}}}}
	b.Put("scene", sceneID, scene)
	if e = pullBatch(ctx, []string{sceneID, "--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	scene.Actions[0].Action.Dimming.Brightness = 35
	b.Put("scene", sceneID, scene)
	if e = pullBatch(ctx, []string{sceneID, "--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	b.Remove("scene", sceneID)
	if e = pullBatch(ctx, []string{sceneID, "--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	b.Remove("room", id)
	if e = pullBatch(ctx, []string{id, "--write"}, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	state, e = stateReader(deps)(ctx)
	if e != nil || bytes.Contains(state, []byte(id)) {
		t.Fatalf("state: %s %v", state, e)
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatalf("bridge mutation: %s %s", r.Method, r.Path)
		}
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Error("provider shutdown timeout")
	}
}
