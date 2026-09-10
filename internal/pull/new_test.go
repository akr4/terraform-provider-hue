package pull

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const newSceneJSON = `{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","metadata":{"name":"夜 ${var.secret}","image":{"rid":"image-id"}},"group":{"rid":"group-id","rtype":"room"},"speed":0,"auto_dynamic":false,"actions":[{"target":{"rid":"light-id","rtype":"light"},"action":{"on":{"on":false},"dimming":{"brightness":0},"color_temperature":{"mirek":null},"color":{"xy":{"x":0.3,"y":0.4}}}}]}`

func TestNewSceneGeneration(t *testing.T) {
	src, err := NewScene([]byte(newSceneJSON), "night", map[string]string{"group-id": "hue_room.bedroom"})
	if err != nil {
		t.Fatal(err)
	}
	f, d := hclsyntax.ParseConfig(src, "new.tf", hcl.InitialPos)
	if d.HasErrors() {
		t.Fatal(d)
	}
	b := f.Body.(*hclsyntax.Body).Blocks[0].Body
	v, d := b.Attributes["name"].Expr.Value(nil)
	if d.HasErrors() || v.AsString() != "夜 ${var.secret}" {
		t.Fatal("template was not escaped")
	}
	for _, part := range []string{"hue_room.bedroom.id", "false", "0", "color_xy"} {
		if !strings.Contains(string(src), part) {
			t.Fatal(string(src))
		}
	}
	if strings.Contains(string(src), "mirek") {
		t.Fatal("null mirek generated")
	}
	src, err = NewScene([]byte(newSceneJSON), "night", nil)
	if err != nil || !strings.Contains(string(src), `"group-id"`) {
		t.Fatalf("%s %v", src, err)
	}
}
func TestNewSceneRejectsUnsupported(t *testing.T) {
	for _, key := range []string{"effects", "gradient"} {
		raw := strings.Replace(newSceneJSON, `"on":{"on":false}`, `"`+key+`":{},"on":{"on":false}`, 1)
		if _, err := NewScene([]byte(raw), "night", nil); err == nil {
			t.Fatal("silently dropped " + key)
		}
	}
}
func TestNewSceneCollision(t *testing.T) {
	dir := t.TempDir()
	path, err := NewResourcePath(dir, "scene", "night")
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteNew(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err = WriteNew(path, []byte("replacement")); err == nil {
		t.Fatal("overwrote file")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Fatal(string(got))
	}
	os.Remove(path)
	os.WriteFile(filepath.Join(dir, "other.tf"), []byte(`resource "hue_scene" "night" {}`), 0600)
	if _, err = NewResourcePath(dir, "scene", "night"); err == nil {
		t.Fatal("duplicate resource")
	}
}
func TestNewInventory(t *testing.T) {
	raw := []byte(`{"resources":[{"mode":"managed","module":"module.x","type":"hue_scene","name":"existing","instances":[{"index_key":0,"attributes":{"id":"scene"}}]},{"mode":"managed","type":"hue_scene","name":"night","instances":[{"attributes":{"id":"other"}}]},{"mode":"managed","type":"hue_room","name":"bedroom","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"group-id"}}]}]}`)
	ids, groups, err := Inventory(raw)
	if err != nil || !ids["scene"] || groups["group-id"] != "hue_room.bedroom" {
		t.Fatalf("%v %v %v", ids, groups, err)
	}
	if !AddressReserved(raw, "scene", "night") || AddressReserved(raw, "scene", "unused") {
		t.Fatal("state address collision detection")
	}
}
