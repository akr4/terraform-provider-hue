package pull

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func testBehavior() hue.BehaviorInstance {
	return hue.BehaviorInstance{ID: sceneID, Type: "behavior_instance", ScriptID: "55555555-5555-4555-8555-555555555555", Enabled: true, Metadata: hue.Metadata{Name: "Switch ${literal}"}, Configuration: json.RawMessage(`{"buttons":{"one":{"scene":"old","long_press":"dim_up"}},"future":{"array":[true,null,9007199254740993]}}`)}
}
func TestBehaviorGenerationAndPull(t *testing.T) {
	b := testBehavior()
	raw, _ := json.Marshal(b)
	src, err := NewBehavior(raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	f, d := hclsyntax.ParseConfig(src, "test.tf", hcl.InitialPos)
	if d.HasErrors() {
		t.Fatal(d)
	}
	v, d := f.Body.(*hclsyntax.Body).Blocks[0].Body.Attributes["configuration"].Expr.Value(&hcl.EvalContext{Functions: map[string]function.Function{"jsonencode": stdlib.JSONEncodeFunc}})
	if d.HasErrors() {
		t.Fatal(d)
	}
	before, _ := configurationValue(b.Configuration)
	actual, _ := configurationValue([]byte(v.AsString()))
	if !before.RawEquals(actual) {
		t.Fatal("generation lost fields/precision")
	}
	baseline := Baseline{ID: b.ID, ScriptID: b.ScriptID, Name: b.Metadata.Name, Enabled: b.Enabled, Configuration: string(b.Configuration)}
	dir := fixture(t, string(src))
	c, err := PrepareBehavior(dir, "test", baseline, b)
	if err != nil || len(c.Edits) != 0 {
		t.Fatalf("round trip: %v", err)
	}
	b.Configuration = json.RawMessage(strings.Replace(string(b.Configuration), `"old"`, `"new"`, 1))
	b.Enabled = false
	c, err = PrepareBehavior(dir, "test", baseline, b)
	if err != nil || len(c.Edits) != 2 {
		t.Fatalf("update: %v", err)
	}
	if _, err = c.Write(); err != nil {
		t.Fatal(err)
	}
	c, err = PrepareBehavior(dir, "test", baseline, b)
	if err != nil || len(c.Edits) != 0 {
		t.Fatalf("idempotence: %v", err)
	}
}
func TestBehaviorPullGuards(t *testing.T) {
	b := testBehavior()
	raw, _ := json.Marshal(b)
	src, _ := NewBehavior(raw, "test")
	baseline := Baseline{ID: b.ID, ScriptID: b.ScriptID, Name: b.Metadata.Name, Enabled: b.Enabled, Configuration: string(b.Configuration)}
	b.Configuration = json.RawMessage(strings.Replace(string(b.Configuration), `"old"`, `"new"`, 1))
	for _, s := range []string{
		strings.Replace(string(src), `"old"`, `hue_scene.test.id`, 1),
		strings.Replace(string(src), `"old"`, `true ? "old" : "other"`, 1),
		strings.Replace(string(src), `"old"`, `"local change"`, 1),
		strings.Replace(string(src), `"old"`, "\"old\" # keep me\n", 1),
		strings.Replace(string(src), `jsonencode(`, `something_else(`, 1),
	} {
		dir := fixture(t, s)
		if _, err := PrepareBehavior(dir, "test", baseline, b); err == nil {
			t.Fatal("accepted unsafe replacement")
		}
		got, _ := os.ReadFile(filepath.Join(dir, "night.tf"))
		if string(got) != s {
			t.Fatal("modified file on error")
		}
	}
	b.ScriptID = "different"
	if _, err := PrepareBehavior(fixture(t, string(src)), "test", baseline, b); err == nil {
		t.Fatal("accepted different script")
	}
}
