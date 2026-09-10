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
)

func TestPullCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const original = `resource "hue_scene" "night" {
 group = hue_room.bedroom.id
 actions = {
  "light" = { brightness = 20, on = true, mirek = 346, color_xy = { x = 0.3, y = 0.4 } } # keep
 }
}
`
	if err := os.WriteFile("night.tf", []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	b := fakebridge.New()
	defer b.Close()
	b.Put("scene", id, hue.Scene{ID: id, Group: hue.Reference{RID: "group"}, Actions: []hue.SceneAction{{Target: hue.Reference{RID: "light", RType: "light"}, Action: hue.Action{On: &hue.On{On: false}, ColorTemperature: &hue.Temperature{Mirek: 400}, Color: &hue.ActionColor{XY: hue.XY{X: 0.5, Y: 0.2}}, Dimming: &hue.Dimming{Brightness: 10}}}}})
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) {
		return []byte(`{"resources":[{"mode":"managed","type":"hue_scene","name":"night","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"` + id + `","group":"group","actions":{"light":{"brightness":20,"on":true,"mirek":346,"color_xy":{"x":0.3,"y":0.4}}}}}]}]}`), nil
	}}
	for _, write := range []bool{false, true} {
		args := []string{"pull", "hue_scene.night"}
		if write {
			args = append(args, "--write")
		}
		var out bytes.Buffer
		if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "20 -> 10") {
			t.Fatal(out.String())
		}
		got, _ := os.ReadFile("night.tf")
		want := original
		if write {
			want = strings.NewReplacer("brightness = 20", "brightness = 10", "on = true", "on = false", "mirek = 346", "mirek = 400", "x = 0.3", "x = 0.5", "y = 0.4", "y = 0.2").Replace(original)
		}
		if string(got) != want {
			t.Fatal(string(got))
		}
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatalf("bridge mutation: %s", r.Method)
		}
	}
}

func TestPullInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"pull"}, {"pull", "hue_room.x"}, {"pull", "hue_scene.x[0]"}, {"pull", "hue_scene.x", "--bad"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal(args)
		}
	}
}

func TestPullNewCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	b := fakebridge.New()
	defer b.Close()
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	b.Put("scene", id, hue.Scene{ID: id, Metadata: hue.Metadata{Name: "New scene"}, Group: hue.Reference{RID: "group", RType: "room"}, Actions: []hue.SceneAction{}})
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return []byte(`{"resources":[]}`), nil }}
	for _, write := range []bool{false, true} {
		args := []string{"pull", "--new", id, "hue_scene.new_scene"}
		if write {
			args = append(args, "--write")
		}
		var out bytes.Buffer
		if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "terraform import 'hue_scene.new_scene'") {
			t.Fatal(out.String())
		}
		_, err := os.Stat("scene_new_scene.tf")
		if (!os.IsNotExist(err)) != write {
			t.Fatal("unexpected file write")
		}
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge mutation")
		}
	}
}

func TestPullNewListing(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	b := fakebridge.New()
	defer b.Close()
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	b.Put("scene", id, hue.Scene{ID: id, Metadata: hue.Metadata{Name: "New candidate"}, Group: hue.Reference{RID: "group", RType: "room"}})
	imported := false
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) {
		if imported {
			return []byte(`{"resources":[{"mode":"managed","module":"module.lights","type":"hue_scene","name":"test","instances":[{"index_key":"new","attributes":{"id":"` + id + `"}}]}]}`), nil
		}
		return []byte(`{"resources":[]}`), nil
	}}
	for _, managed := range []bool{false, true} {
		imported = managed
		var out bytes.Buffer
		if err := runWith(context.Background(), []string{"pull", "--new"}, &out, &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "New candidate") == managed {
			t.Fatal(out.String())
		}
	}
	entries, _ := os.ReadDir(".")
	if len(entries) != 0 {
		t.Fatal("listing created files")
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge mutation")
		}
	}
}

func TestPullGroupCLI(t *testing.T) {
	for _, kind := range []string{"room", "zone"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
			const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			src := `resource "hue_` + kind + `" "test" {
 name = "Old"
 archetype = "other"
 children = []
}
`
			if err := os.WriteFile("group.tf", []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			b := fakebridge.New()
			defer b.Close()
			b.Put(kind, id, hue.Group{ID: id, Type: kind, Metadata: hue.Metadata{Name: "New", Archetype: "other"}})
			deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) {
				return []byte(`{"resources":[{"mode":"managed","type":"hue_` + kind + `","name":"test","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"` + id + `","name":"Old","archetype":"other","children":[]}}]}]}`), nil
			}}
			for _, write := range []bool{false, true} {
				args := []string{"pull", "hue_" + kind + ".test"}
				if write {
					args = append(args, "--write")
				}
				var out bytes.Buffer
				if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), `.name: "Old" -> "New"`) {
					t.Fatal(out.String())
				}
				got, _ := os.ReadFile("group.tf")
				want := src
				if write {
					want = strings.Replace(src, `"Old"`, `"New"`, 1)
				}
				if string(got) != want {
					t.Fatal(string(got))
				}
			}
			for _, r := range b.Requests() {
				if r.Method != "GET" || r.Path != "/clip/v2/resource/"+kind+"/"+id {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestPullNewGroupCLI(t *testing.T) {
	for _, kind := range []string{"room", "zone"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
			b := fakebridge.New()
			defer b.Close()
			const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			b.Put(kind, id, hue.Group{ID: id, Type: kind, Metadata: hue.Metadata{Name: "New group candidate", Archetype: "other"}})
			deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return []byte(`{"resources":[]}`), nil }}
			var list bytes.Buffer
			if err := runWith(context.Background(), []string{"pull", "--new"}, &list, &bytes.Buffer{}, deps); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(list.String(), "New group candidate") {
				t.Fatal(list.String())
			}
			path := kind + "_new_group.tf"
			for _, write := range []bool{false, true} {
				args := []string{"pull", "--new", id, "hue_" + kind + ".new_group"}
				if write {
					args = append(args, "--write")
				}
				var out bytes.Buffer
				if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "terraform import 'hue_"+kind+".new_group'") {
					t.Fatal(out.String())
				}
				_, err := os.Stat(path)
				if (!os.IsNotExist(err)) != write {
					t.Fatal("unexpected file write")
				}
			}
			if err := runWith(context.Background(), []string{"pull", "--new", id, "hue_" + kind + ".new_group", "--write"}, &bytes.Buffer{}, &bytes.Buffer{}, deps); err == nil {
				t.Fatal("overwrote definition")
			}
			for _, r := range b.Requests() {
				if r.Method != "GET" {
					t.Fatal("bridge mutation")
				}
			}
		})
	}
}

func TestPullColorModeCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const src = `resource "hue_scene" "night" {
 group = hue_room.bedroom.id
 actions = {
  "light" = { mirek = 346 } # saved color
 }
}
`
	if err := os.WriteFile("night.tf", []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	b := fakebridge.New()
	defer b.Close()
	b.Put("scene", id, json.RawMessage(`{"id":"`+id+`","group":{"rid":"group","rtype":"room"},"actions":[{"target":{"rid":"light","rtype":"light"},"action":{"color_temperature":{"mirek":null},"color":{"xy":{"x":0.3,"y":0.4}}}}]}`))
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) {
		return []byte(`{"resources":[{"mode":"managed","type":"hue_scene","name":"night","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"` + id + `","group":"group","actions":{"light":{"mirek":346,"kelvin":2890}}}}]}]}`), nil
	}}
	for _, write := range []bool{false, true} {
		var out bytes.Buffer
		args := []string{"pull", "hue_scene.night"}
		if write {
			args = append(args, "--write")
		}
		if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile("night.tf")
		if write {
			if !strings.Contains(string(got), "color_xy") || strings.Contains(string(got), "mirek") {
				t.Fatal(string(got))
			}
		} else {
			if string(got) != src {
				t.Fatal("preview modified file")
			}
		}
	}
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"pull", "hue_scene.night"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No supported resource changes.") {
		t.Fatal(out.String())
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge mutation")
		}
	}
}
