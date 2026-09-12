package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"os"
	"strings"
	"testing"
)

func TestPullMultipleSelectors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	const old = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const added = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	src := []byte("resource \"hue_room\" \"room\" {\n name = \"Old\"\n archetype = \"bedroom\"\n children = []\n}\n")
	if err := os.WriteFile("room.tf", src, 0600); err != nil {
		t.Fatal(err)
	}
	b := fakebridge.New()
	defer b.Close()
	b.Put("room", old, hue.Group{ID: old, Type: "room", Metadata: hue.Metadata{Name: "Changed", Archetype: "bedroom"}})
	b.Put("room", added, hue.Group{ID: added, Type: "room", Metadata: hue.Metadata{Name: "New"}})
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }, readState: func(context.Context) ([]byte, error) { return batchState(old, "Old"), nil }, terraform: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	var out bytes.Buffer
	if err := pullBatch(context.Background(), []string{"hue_room.room", added, "missing", "--write"}, &out, deps); err == nil {
		t.Fatal("missing selector did not block write")
	}
	if got, _ := os.ReadFile("room.tf"); !bytes.Equal(got, src) {
		t.Fatal("partial write")
	}
	if _, err := os.Stat("imports_hue.tf"); !os.IsNotExist(err) {
		t.Fatal("partial import write")
	}
	out.Reset()
	if err := pullBatch(context.Background(), []string{"hue_room.room", old, added, added, "--write"}, &out, deps); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile("room.tf")
	if !strings.Contains(string(got), "Changed") {
		t.Fatal("existing resource not updated")
	}
	imports, _ := os.ReadFile("imports_hue.tf")
	if strings.Count(string(imports), "import {") != 1 || !strings.Contains(string(imports), added) {
		t.Fatal(string(imports))
	}
	var checkpoint pullCheckpoint
	raw, _ := os.ReadFile(".hue-pull-baseline.json")
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	for _, r := range b.Requests() {
		if r.Method != "GET" {
			t.Fatal("bridge write")
		}
	}
}
func TestParsePullMultiple(t *testing.T) {
	o, err := parsePullOptions([]string{"one", "--module", "upstairs.room", "two", "--write", "hue_scene.three"}, true)
	if err != nil || len(o.selectors) != 3 || !o.write || o.module != "module.upstairs.module.room." {
		t.Fatalf("%+v %v", o, err)
	}
}
