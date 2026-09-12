package pull

import (
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
}
