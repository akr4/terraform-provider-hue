package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	hueprovider "github.com/akr4/terraform-provider-hue/internal/provider"
	"github.com/akr4/terraform-provider-hue/internal/pull"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-framework/function"
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

// Functions forwards the provider's functions. The embedded provider.Provider
// interface does not promote them.
func (p *pullTestProvider) Functions(ctx context.Context) []func() function.Function {
	return p.Provider.(provider.ProviderWithFunctions).Functions(ctx)
}

// startAccTerraform serves the provider in-process against b and returns a
// runner for the real terraform binary in a new temporary working directory.
func startAccTerraform(t *testing.T, b *fakebridge.Bridge) (string, func(context.Context, ...string) ([]byte, error)) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 for Terraform integration")
	}
	tf := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if tf == "" {
		var err error
		tf, err = exec.LookPath("terraform")
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TF_DATA_DIR", filepath.Join(root, ".terraform"))
	t.Setenv("TF_WORKSPACE", "default")
	t.Setenv("HUE_BRIDGE_HOST", "test-bridge")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	t.Setenv("TF_CLI_ARGS", "")
	t.Setenv("TF_CLI_ARGS_plan", "")
	t.Setenv("TF_CLI_ARGS_apply", "")
	ctx, cancel := context.WithCancel(context.Background())
	config := make(chan *plugin.ReattachConfig)
	closed := make(chan struct{})
	go func() {
		_ = tf6server.Serve("registry.terraform.io/akr4/hue", func() tfprotov6.ProviderServer {
			return providerserver.NewProtocol6(&pullTestProvider{Provider: hueprovider.New("test")(), client: b.Client()})()
		}, tf6server.WithDebug(ctx, config, closed))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Error("provider shutdown timeout")
		}
	})
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
	return root, func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, tf, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		if e != nil {
			t.Log(stderr.String())
		}
		return out, e
	}
}

// TestAccPullRoundTrip exercises file preparation followed by explicit Terraform plan/apply;
// a test provider transports every API request to an in-memory HTTPS bridge.
func TestAccPullRoundTrip(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	root, run := startAccTerraform(t, b)
	ctx := context.Background()
	var e error
	if e = os.WriteFile("main.tf", []byte("terraform {\n required_providers {\n hue = { source = \"akr4/hue\" }\n }\n}\nprovider \"hue\" {}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	deps := dependencies{terraform: func(ctx context.Context, args ...string) ([]byte, error) {
		if !((len(args) == 2 && args[0] == "state" && args[1] == "pull") || (len(args) == 2 && args[0] == "show" && args[1] == "-json")) {
			t.Fatalf("pull invoked unexpected Terraform command: %v", args)
		}
		return run(ctx, args...)
	}, newClient: func(string, string, string) (*hue.Client, error) { return b.Client(), nil }}
	// These calls represent the user running vanilla Terraform separately.
	apply := func() {
		path := filepath.Join(root, "user.tfplan")
		if _, e := run(ctx, "plan", "-input=false", "-out="+path); e != nil {
			t.Fatal(e)
		}
		data, e := run(ctx, "show", "-json", path)
		if e != nil {
			t.Fatal(e)
		}
		var plan struct {
			ResourceChanges []struct{ Change struct{ Actions []string } } `json:"resource_changes"`
		}
		if e = json.Unmarshal(data, &plan); e != nil {
			t.Fatal(e)
		}
		for _, r := range plan.ResourceChanges {
			for _, a := range r.Change.Actions {
				if a != "no-op" {
					t.Fatalf("unexpected resource mutation: %s", data)
				}
			}
		}
		if _, e = run(ctx, "apply", "-input=false", path); e != nil {
			t.Fatal(e)
		}
	}
	write := func(args ...string) {
		before, e := stateReader(deps)(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var out bytes.Buffer
		if e = importBlocks(ctx, args, &out, deps); e != nil {
			t.Fatal(e, out.String())
		}
		after, e := stateReader(deps)(ctx)
		if e != nil || !bytes.Equal(before, after) {
			t.Fatalf("pull changed state: %v", e)
		}
	}

	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	group := hue.Group{ID: id, Type: "room", Metadata: hue.Metadata{Name: "Imported", Archetype: "bedroom"}, Children: []hue.Reference{}}
	b.Put("room", id, group)
	var output bytes.Buffer
	if e = importBlocks(ctx, nil, &output, deps); e != nil {
		t.Fatal(e, output.String())
	}
	if _, e = os.Stat("room_Imported.tf"); !os.IsNotExist(e) {
		t.Fatal("preview created definition")
	}
	write("--write")
	write("--write") // Pending import must not produce duplicate definitions.
	if _, e = os.Stat("room_Imported.tf"); !os.IsNotExist(e) {
		t.Fatal("pull generated a resource definition")
	}
	if _, e = run(ctx, "plan", "-input=false", "-generate-config-out=generated.tf"); e != nil {
		t.Fatal(e)
	}
	write("--write") // Generated but not yet imported definitions remain untouched.
	apply()
	state, e := stateReader(deps)(ctx)
	if e != nil || !bytes.Contains(state, []byte(id)) {
		t.Fatalf("state: %s %v", state, e)
	}
	contents, e := os.ReadFile("generated.tf")
	if e != nil {
		t.Fatal(e)
	}
	group.Metadata.Name = "From app"
	b.Put("room", id, group)
	write(id, "--write")
	afterPull, e := os.ReadFile("generated.tf")
	if e != nil || !bytes.Equal(contents, afterPull) {
		t.Fatal("pull changed managed definition", e)
	}
	// Restore the fixture for subsequent import checks without mutating real lights.
	group.Metadata.Name = "Imported"
	b.Put("room", id, group)
	sceneID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	scene := hue.Scene{ID: sceneID, Type: "scene", Metadata: hue.Metadata{Name: "Evening"}, Group: hue.Reference{RID: id, RType: "room"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: fakebridge.LightID, RType: "light"}, Action: hue.Action{On: &hue.On{On: true}, Dimming: &hue.Dimming{Brightness: 20}, ColorTemperature: &hue.Temperature{Mirek: 350}}}}}
	b.Put("scene", sceneID, scene)
	write(sceneID, "--write")
	_, genErr := run(ctx, "plan", "-input=false", "-generate-config-out=generated_scene.tf")
	generated, e := os.ReadFile("generated_scene.tf")
	if e != nil {
		t.Fatal(genErr, e)
	}
	// The color temperature is generated as mirek. kelvin is an error.
	if regexp.MustCompile(`(?m)^\s*kelvin\s*=`).Match(generated) {
		t.Fatalf("generated kelvin:\n%s", generated)
	}
	generated = regexp.MustCompile(`(?m)^\s*color_hex\s*=.*\n`).ReplaceAll(generated, nil)
	if e = os.WriteFile("generated_scene.tf", generated, 0600); e != nil {
		t.Fatal(e)
	}
	apply()
	// Simulate a version-0 state locally, then let Terraform perform the upgrade.
	legacy, e := stateReader(deps)(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var legacyState map[string]any
	if e = json.Unmarshal(legacy, &legacyState); e != nil {
		t.Fatal(e)
	}
	for _, item := range legacyState["resources"].([]any) {
		r := item.(map[string]any)
		if r["type"] != "hue_scene" {
			continue
		}
		for _, instance := range r["instances"].([]any) {
			i := instance.(map[string]any)
			i["schema_version"] = float64(0)
			attrs := i["attributes"].(map[string]any)
			for _, action := range attrs["actions"].(map[string]any) {
				action.(map[string]any)["color_hex"] = nil
			}
		}
	}
	legacy, _ = json.Marshal(legacyState)
	if e = os.WriteFile("terraform.tfstate", legacy, 0600); e != nil {
		t.Fatal(e)
	}
	apply()
	planJSON, e := run(ctx, "show", "-json", filepath.Join(root, "user.tfplan"))
	if e != nil {
		t.Fatal(e)
	}
	var html bytes.Buffer
	if e = previewCommand([]string{"--html"}, bytes.NewReader(planJSON), &html); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(html.Bytes(), []byte("Evening")) || bytes.Contains(html.Bytes(), []byte("ZgotmplZ")) {
		t.Fatal("invalid evaluated plan preview")
	}

	smartID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	start, _ := hue.ParseSmartStart("00:00:00")
	smart := hue.SmartScene{ID: smartID, Type: "smart_scene", Metadata: hue.Metadata{Name: "Natural"}, Group: hue.Reference{RID: id, RType: "room"}, TransitionDuration: 60000, State: "inactive", WeekTimeslots: []hue.SmartDay{{Recurrence: []string{"monday", "sunday"}, Timeslots: []hue.SmartSlot{{StartTime: start, Target: hue.Reference{RID: sceneID, RType: "scene"}}}}}}
	b.Put("smart_scene", smartID, smart)
	write(smartID, "--write")
	if _, e = run(ctx, "plan", "-input=false", "-generate-config-out=generated_smart.tf"); e != nil {
		t.Fatal(e)
	}
	apply()
	beforeRemoval, e := os.ReadFile("generated_smart.tf")
	if e != nil {
		t.Fatal(e)
	}
	b.Remove("smart_scene", smartID)
	write(smartID, "--write")
	afterRemoval, e := os.ReadFile("generated_smart.tf")
	if e != nil || !bytes.Equal(beforeRemoval, afterRemoval) {
		t.Fatal("pull deleted managed definition", e)
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatalf("bridge mutation: %s %s", r.Method, r.Path)
		}
	}
}

// TestAccImportBlocksComplexConfiguration reads a state that Terraform itself
// wrote for count, for_each, a provider alias, a hue_device, and modules that
// share a source or use count. import-blocks used to stop on each of them.
func TestAccImportBlocksComplexConfiguration(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	_, run := startAccTerraform(t, b)
	ctx := context.Background()
	required := "terraform {\n required_providers {\n hue = { source = \"akr4/hue\" }\n }\n}\n"
	files := map[string]string{
		"main.tf": required + `provider "hue" {}
provider "hue" {
  alias = "second"
}
resource "hue_device" "lamp" {
  device_id = "` + fakebridge.DeviceID + `"
}
resource "hue_zone" "counted" {
  count    = 1
  name     = "Counted"
  children = ["` + fakebridge.LightID + `"]
}
resource "hue_zone" "each" {
  for_each = toset(["day"])
  name     = "Each ${each.key}"
  children = ["` + fakebridge.LightID + `"]
}
resource "hue_zone" "aliased" {
  provider = hue.second
  name     = "Aliased"
  children = ["` + fakebridge.LightID + `"]
}
module "a" {
  source = "./zone"
  name   = "Module A"
}
module "b" {
  source = "./zone"
  name   = "Module B"
}
module "counted" {
  source = "./zone"
  count  = 1
  name   = "Module counted"
}
`,
		"zone/main.tf": required + `variable "name" {
  type = string
}
resource "hue_zone" "this" {
  name     = var.name
  children = ["` + fakebridge.LightID + `"]
}
`,
	}
	for path, src := range files {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(src), 0600); e != nil {
			t.Fatal(e)
		}
	}
	// Both provider instances share the in-process server, whose
	// ConfigureProvider is not safe to call concurrently. Configure them one at
	// a time.
	for _, args := range [][]string{{"get"}, {"apply", "-input=false", "-auto-approve", "-parallelism=1"}} {
		if _, e := run(ctx, args...); e != nil {
			t.Fatal(args, e)
		}
	}
	deps := dependencies{terraform: run, newClient: func(string, string, string) (*hue.Client, error) { return b.Client(), nil }}
	state, e := stateReader(deps)(ctx)
	if e != nil {
		t.Fatal(e)
	}
	managed, e := pull.ManagedResources(state)
	if e != nil {
		t.Fatal(e)
	}
	var addresses []string
	for _, r := range managed {
		addresses = append(addresses, r.Address)
	}
	want := `hue_zone.aliased hue_zone.counted[0] hue_zone.each["day"] module.a.hue_zone.this module.b.hue_zone.this module.counted[0].hue_zone.this`
	if strings.Join(addresses, " ") != want {
		t.Fatalf("got %v", addresses)
	}
	const newID = "90000000-0000-4000-8000-000000000000"
	b.Put("zone", newID, hue.Group{ID: newID, Type: "zone", Metadata: hue.Metadata{Name: "Unmanaged"}, Children: []hue.Reference{}})
	var out bytes.Buffer
	if e = importBlocks(ctx, nil, &out, deps); e != nil {
		t.Fatal(e, out.String())
	}
	if !strings.Contains(out.String(), "Prepare import hue_zone.Unmanaged ["+newID+"]") {
		t.Fatal(out.String())
	}
	for _, r := range managed {
		if strings.Contains(out.String(), r.ID) {
			t.Fatalf("proposed managed %s: %s", r.Address, out.String())
		}
	}
}
