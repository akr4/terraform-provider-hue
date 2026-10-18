package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
)

// terraformCLI serves the provider for b in this process and returns a working
// directory, with the provider configured, and a function that runs the
// Terraform CLI there.
func terraformCLI(t *testing.T, b *fakebridge.Bridge) (string, func(args ...string) ([]byte, error)) {
	t.Helper()
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run Terraform CLI integration")
	}
	binary := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("terraform")
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	configs := make(chan *plugin.ReattachConfig, 1)
	closed := make(chan struct{})
	served := make(chan error, 1)
	const address = "registry.terraform.io/akr4/hue"
	go func() {
		served <- tf6server.Serve(address, providerserver.NewProtocol6(&hueProvider{version: "test", client: b.Client()}), tf6server.WithDebug(ctx, configs, closed), tf6server.WithLoggingSink(t))
	}()
	var cfg *plugin.ReattachConfig
	select {
	case cfg = <-configs:
	case err := <-served:
		cancel()
		t.Fatalf("provider startup: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("provider did not stop")
		}
	})
	reattach, _ := json.Marshal(map[string]any{address: map[string]any{"Protocol": cfg.Protocol, "ProtocolVersion": 6, "Pid": cfg.Pid, "Test": true, "Addr": map[string]string{"Network": cfg.Addr.Network(), "String": cfg.Addr.String()}}})
	dir := t.TempDir()
	for name, body := range map[string]string{
		"terraform.rc": fmt.Sprintf("provider_installation {\n dev_overrides {\n \"akr4/hue\" = %q\n }\n}\n", dir),
		"provider.tf": `terraform {
 required_providers {
 hue = { source = "akr4/hue" }
 }
}` + "\n" + accProvider,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "TF_") && !strings.HasPrefix(e, "HUE_") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "TF_CLI_CONFIG_FILE="+filepath.Join(dir, "terraform.rc"), "TF_REATTACH_PROVIDERS="+string(reattach))
		return cmd.CombinedOutput()
	}
}
