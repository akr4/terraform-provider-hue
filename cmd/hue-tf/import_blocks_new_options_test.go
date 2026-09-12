package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func TestPullNewOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--write"}, {"--module", "room"}, {"uuid", "--module"},
		{"uuid", "--module", "a..b"}, {"uuid", "--module", "a[0]"},
		{"uuid", "hue_scene.x", "--module", "a"}, {"uuid", "--bad"},
		{"uuid", "--write", "--write"}, {"uuid", "--module", "a", "--module", "b"},
	} {
		if _, err := parsePullNewArgs(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	o, err := parsePullNewArgs([]string{"uuid", "--write", "--module", "downstairs.washroom"})
	if err != nil || !o.write || o.module != "module.downstairs.module.washroom." || o.id != "uuid" {
		t.Fatalf("%+v %v", o, err)
	}
	for input, want := range map[string]string{"島の温もり": "島の温もり", "Hue Smart button 2": "Hue_Smart_button_2", "123 scene": "resource_123_scene", "": "resource", "a/b": "a_b"} {
		if got := pullResourceName(input); got != want {
			t.Errorf("%q: %q != %q", input, got, want)
		}
	}
}

func TestPullNewAutomaticDestination(t *testing.T) {
	for _, kind := range []string{"room", "zone", "scene", "behavior_instance"} {
		for _, module := range []string{"", "downstairs.washroom"} {
			t.Run(kind+"/"+module, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
				destination := root
				prefix := ""
				if module != "" {
					destination = filepath.Join(root, "unrelated", "destination")
					if err := os.MkdirAll(destination, 0700); err != nil {
						t.Fatal(err)
					}
					for path, source := range map[string]string{
						filepath.Join(root, "main.tf"):              `module "downstairs" { source = "./unrelated" }`,
						filepath.Join(root, "unrelated", "main.tf"): `module "washroom" { source = "./destination" }`,
					} {
						if err := os.WriteFile(path, []byte(source), 0600); err != nil {
							t.Fatal(err)
						}
					}
					prefix = "module.downstairs.module.washroom."
				}
				b := fakebridge.New()
				defer b.Close()
				id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
				b.Put(kind, id, map[string]any{
					"id": id, "type": kind, "metadata": map[string]any{"name": "島の温もり"},
					"group":   map[string]any{"rid": "group", "rtype": "room"},
					"actions": []any{}, "children": []any{}, "enabled": true,
					"script_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "configuration": map[string]any{},
				})
				// Later collection reads must not overwrite the selected raw JSON.
				for _, other := range []string{"room", "zone", "scene", "behavior_instance"} {
					if other != kind {
						b.Put(other, "other", map[string]any{"id": "other", "type": other, "metadata": map[string]any{"name": "Unrelated"}})
					}
				}
				deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return []byte(`{"resources":[]}`), nil }}
				args := []string{"import-blocks", "--new", id}
				if module != "" {
					args = append(args, "--module", module)
				}
				path := filepath.Join(destination, kind+"_島の温もり.tf")
				for _, write := range []bool{false, true} {
					call := append([]string{}, args...)
					if write {
						call = append(call, "--write")
					}
					var out bytes.Buffer
					if err := runWith(context.Background(), call, &out, &bytes.Buffer{}, deps); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(out.String(), "to = "+prefix+"hue_"+kind+".島の温もり") {
						t.Fatal(out.String())
					}
					_, err := os.Stat(path)
					if !os.IsNotExist(err) {
						t.Fatal("pull created resource definition")
					}
					if !write && !os.IsNotExist(err) {
						t.Fatalf("preview wrote file: %v", err)
					}
				}
				if err := runWith(context.Background(), append(args, "--write"), &bytes.Buffer{}, &bytes.Buffer{}, deps); err != nil {
					t.Fatal(err)
				}
				for _, r := range b.Requests() {
					if r.Method != "GET" {
						t.Fatalf("bridge mutation: %s", r.Method)
					}
				}
			})
		}
	}
}
