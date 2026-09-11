package pull

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func ResourceAddress(address string) (kind, name string, err error) {
	parts := strings.Split(address, ".")
	if len(parts) < 2 || len(parts)%2 != 0 {
		return "", "", fmt.Errorf("expected [module.NAME.]hue_TYPE.NAME")
	}
	for i := 0; i < len(parts)-2; i += 2 {
		if parts[i] != "module" || !hclsyntax.ValidIdentifier(parts[i+1]) {
			return "", "", fmt.Errorf("only unindexed module addresses are supported")
		}
	}
	parts = parts[len(parts)-2:]
	if !hclsyntax.ValidIdentifier(parts[1]) {
		return "", "", fmt.Errorf("invalid resource name")
	}
	kind = strings.TrimPrefix(parts[0], "hue_")
	if parts[0] != "hue_"+kind || (kind != "room" && kind != "zone" && kind != "scene" && kind != "smart_scene" && kind != "behavior_instance") {
		return "", "", fmt.Errorf("unsupported resource type")
	}
	return kind, parts[1], nil
}
func childIDs(kind string, g hue.Group) ([]string, error) {
	childKind := "device"
	if kind == "zone" {
		childKind = "light"
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, c := range g.Children {
		if c.RType != childKind || c.RID == "" || seen[c.RID] {
			return nil, fmt.Errorf("invalid or duplicate %s child", childKind)
		}
		ids = append(ids, c.RID)
		seen[c.RID] = true
	}
	return ids, nil
}
func stringLiteral(expr hclsyntax.Expression) (cty.Value, error) {
	t, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok || !t.IsStringLiteral() {
		return cty.NilVal, fmt.Errorf("expected a literal string; expressions are not overwritten")
	}
	v, d := expr.Value(nil)
	if d.HasErrors() {
		return cty.NilVal, fmt.Errorf("invalid string")
	}
	return v, nil
}
func sameChildren(a, b []string) bool {
	x := append([]string{}, a...)
	y := append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
func (c *Change) valueEdit(expr hclsyntax.Expression, value cty.Value, field string) {
	r := expr.Range()
	c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), After: string(hclwrite.TokensForValue(value).Bytes()), Field: field})
}
func (c *Change) finish() {
	sort.Slice(c.Edits, func(i, j int) bool { return c.Edits[i].Start < c.Edits[j].Start })
	c.Updated = append([]byte(nil), c.Original...)
	for i := len(c.Edits) - 1; i >= 0; i-- {
		e := c.Edits[i]
		next := append([]byte(nil), c.Updated[:e.Start]...)
		next = append(next, e.After...)
		next = append(next, c.Updated[e.End:]...)
		c.Updated = next
	}
}

func PrepareGroup(dir, kind, name string, baseline Baseline, g hue.Group) (*Change, error) {
	if (kind != "room" && kind != "zone") || g.ID != baseline.ID || (g.Type != "" && g.Type != kind) {
		return nil, fmt.Errorf("group identity/type differs from state")
	}
	ids, err := childIDs(kind, g)
	if err != nil {
		return nil, err
	}
	c, b, err := findResource(dir, kind, name)
	if err != nil {
		return nil, err
	}
	for _, field := range []struct{ key, old, target string }{{"name", baseline.Name, g.Metadata.Name}, {"archetype", baseline.Archetype, g.Metadata.Archetype}} {
		attr := b.Body.Attributes[field.key]
		if attr == nil {
			return nil, fmt.Errorf("%s must be explicitly configured", field.key)
		}
		local, err := stringLiteral(attr.Expr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.key, err)
		}
		if local.AsString() == field.target {
			continue
		}
		if local.AsString() != field.old {
			return nil, fmt.Errorf("local %s differs from state; resolve the local edit first", field.key)
		}
		c.valueEdit(attr.Expr, cty.StringVal(field.target), field.key)
	}
	attr := b.Body.Attributes["children"]
	if attr == nil {
		return nil, fmt.Errorf("children must be explicitly configured")
	}
	tuple, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, fmt.Errorf("children must be a literal list")
	}
	local := []string{}
	for _, expr := range tuple.Exprs {
		v, err := stringLiteral(expr)
		if err != nil {
			return nil, fmt.Errorf("children: %w", err)
		}
		local = append(local, v.AsString())
	}
	if !sameChildren(local, ids) {
		if !sameChildren(local, baseline.Children) {
			return nil, fmt.Errorf("local children differ from state; resolve the local edit first")
		}
		tokens, d := hclsyntax.LexExpression(attr.Expr.Range().SliceBytes(c.Original), c.Path, attr.Expr.Range().Start)
		if d.HasErrors() {
			return nil, fmt.Errorf("cannot parse children")
		}
		for _, t := range tokens {
			if t.Type == hclsyntax.TokenComment {
				return nil, fmt.Errorf("children contains comments; update membership manually to preserve annotations")
			}
		}
		values := []cty.Value{}
		for _, id := range ids {
			values = append(values, cty.StringVal(id))
		}
		c.valueEdit(attr.Expr, cty.TupleVal(values), "children")
	}
	c.finish()
	return c, nil
}

func NewGroup(raw json.RawMessage, kind, name string) ([]byte, error) {
	if (kind != "room" && kind != "zone") || !hclsyntax.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid group type/name")
	}
	var g hue.Group
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, err
	}
	if g.ID == "" || g.Metadata.Name == "" || (g.Type != "" && g.Type != kind) {
		return nil, fmt.Errorf("invalid group")
	}
	ids, err := childIDs(kind, g)
	if err != nil {
		return nil, err
	}
	f := hclwrite.NewEmptyFile()
	b := f.Body().AppendNewBlock("resource", []string{"hue_" + kind, name}).Body()
	b.SetAttributeValue("name", cty.StringVal(g.Metadata.Name))
	b.SetAttributeValue("archetype", cty.StringVal(g.Metadata.Archetype))
	values := []cty.Value{}
	for _, id := range ids {
		values = append(values, cty.StringVal(id))
	}
	b.SetAttributeValue("children", cty.TupleVal(values))
	b.AppendNewBlock("lifecycle", nil).Body().SetAttributeValue("prevent_destroy", cty.True)
	return f.Bytes(), nil
}
