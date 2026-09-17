package pull

import (
	"strings"
	"testing"
)

func TestSceneJSONActions(t *testing.T) {
	raw := strings.Replace(newSceneJSON, `"on":{"on":false}`, `"gradient":{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}}],"mode":"interpolated_palette"},"effects":{"effect":"fire"},"effects_v2":{"action":{"effect":"prism","parameters":{"speed":0.4}}},"dynamics":{"duration":800},"on":{"on":false}`, 1)
	src, err := NewScene([]byte(raw), "night", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "jsonencode(") || !strings.Contains(string(src), "interpolated_palette") || !strings.Contains(string(src), "fire") || !strings.Contains(string(src), "effects_v2") || !strings.Contains(string(src), "prism") || !strings.Contains(string(src), "duration") {
		t.Fatal(string(src))
	}
}
