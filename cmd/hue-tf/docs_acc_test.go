package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
)

// readmeGettingStarted returns the first HCL block in the README's Getting
// started section.
func readmeGettingStarted(t *testing.T) string {
	t.Helper()
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(readme), "\n## Getting started\n")
	section, _, _ = strings.Cut(section, "\n## ")
	_, block, found := strings.Cut(section, "```hcl\n")
	config, _, closed := strings.Cut(block, "```")
	if !ok || !found || !closed {
		t.Fatal("the README has no HCL block under Getting started")
	}
	return config
}

// TestAccGettingStarted applies and destroys the README's first configuration.
// It must change nothing on the bridge but its own zone and scene.
func TestAccGettingStarted(t *testing.T) {
	config := readmeGettingStarted(t)
	b := fakebridge.New()
	defer b.Close()
	zones, scenes := b.Count("zone"), b.Count("scene")
	_, run := startAccTerraform(t, b)
	if err := os.WriteFile("main.tf", []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	terraform := func(args ...string) string {
		t.Helper()
		out, err := run(ctx, args...)
		if err != nil {
			t.Fatalf("terraform %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}

	terraform("apply", "-input=false", "-auto-approve")
	if b.Count("zone") != zones+1 || b.Count("scene") != scenes+1 {
		t.Fatalf("apply left %d zones and %d scenes, want %d and %d", b.Count("zone"), b.Count("scene"), zones+1, scenes+1)
	}
	// The README passes this output to hue-tf recall.
	id := strings.TrimSpace(terraform("output", "-raw", "scene_id"))
	scene, err := b.ReadScene(ctx, id)
	if err != nil {
		t.Fatalf("scene_id %q: %v", id, err)
	}
	if scene.Group.RType != "zone" {
		t.Fatalf("the scene belongs to a %s, want a zone", scene.Group.RType)
	}

	terraform("destroy", "-input=false", "-auto-approve")
	if b.Count("zone") != zones || b.Count("scene") != scenes {
		t.Fatalf("destroy left %d zones and %d scenes, want %d and %d", b.Count("zone"), b.Count("scene"), zones, scenes)
	}
	for _, req := range b.Requests() {
		if req.Method != "GET" && !strings.HasPrefix(req.Path, "/clip/v2/resource/zone") && !strings.HasPrefix(req.Path, "/clip/v2/resource/scene") {
			t.Errorf("the walkthrough changed more than its zone and scene: %s %s", req.Method, req.Path)
		}
	}
}

// TestAccDocExamples validates every example that the reference docs render.
func TestAccDocExamples(t *testing.T) {
	var files []string
	for _, pattern := range []string{"provider/provider*.tf", "resources/*/resource*.tf", "data-sources/*/data-source*.tf", "functions/*/function*.tf"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", "examples", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no examples found")
	}
	examples := map[string][]byte{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		// Resource and data source examples leave out the provider source.
		if !bytes.Contains(body, []byte("required_providers")) {
			body = append([]byte("terraform {\n required_providers {\n hue = { source = \"akr4/hue\" }\n }\n}\n"), body...)
		}
		examples[file] = body
	}
	b := fakebridge.New()
	defer b.Close()
	root, run := startAccTerraform(t, b)
	for i, file := range files {
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), examples[file], 0600); err != nil {
			t.Fatal(err)
		}
		out, err := run(context.Background(), "-chdir="+dir, "validate", "-json")
		var result struct {
			Diagnostics []struct{ Severity, Summary, Detail string }
		}
		if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
			t.Fatalf("%s: %v; %v", file, err, jsonErr)
		}
		for _, d := range result.Diagnostics {
			// An example may refer to resources that other examples declare.
			if d.Severity == "error" && d.Summary != "Reference to undeclared resource" {
				t.Errorf("%s: %s: %s", file, d.Summary, d.Detail)
			}
		}
	}
}
