package pull

import (
	"os"
	"strings"
	"testing"
)

func TestSceneJSONActions(t *testing.T) {
	raw := strings.Replace(newSceneJSON, `"on":{"on":false}`, `"gradient":{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}}],"mode":"interpolated_palette"},"effects":{"effect":"fire"},"on":{"on":false}`, 1)
	src, err := NewScene([]byte(raw), "night", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "jsonencode(") || !strings.Contains(string(src), "interpolated_palette") || !strings.Contains(string(src), "fire") {
		t.Fatal(string(src))
	}
	baseline, err := CanonicalAttributes(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(dir+"/scene.tf", src, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := NewScene([]byte(strings.Replace(raw, `"fire"`, `"candle"`, 1)), "night", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := PrepareSync(dir, "scene", "night", baseline, next, StateContext(nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.Updated), "candle") || !strings.Contains(string(c.Updated), "interpolated_palette") {
		t.Fatal(string(c.Updated))
	}
	// Existing state/config from before these fields existed gets a readable addition.
	old, err := NewScene([]byte(newSceneJSON), "night", nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err = CanonicalAttributes(old)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dir+"/scene.tf", old, 0600); err != nil {
		t.Fatal(err)
	}
	c, err = PrepareSync(dir, "scene", "night", baseline, src, StateContext(nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.Updated), "jsonencode(") {
		t.Fatal(string(c.Updated))
	}
	if _, err = CombineChanges([]*Change{c}); err != nil {
		t.Fatal(err)
	}
}
