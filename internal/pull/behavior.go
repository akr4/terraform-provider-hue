package pull

import (
	"encoding/json"
	"fmt"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

func configurationValue(raw []byte) (cty.Value, error) {
	if err := hue.ValidateConfiguration(raw); err != nil {
		return cty.NilVal, err
	}
	ty, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(raw, ty)
}

// NewBehavior preserves the complete script configuration, including fields
// this CLI does not understand. Runtime-only fields are never emitted.
func NewBehavior(raw json.RawMessage, name string) ([]byte, error) {
	if !hclsyntax.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid resource name")
	}
	var b hue.BehaviorInstance
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	if b.ID == "" || b.Type != "behavior_instance" || b.ScriptID == "" || b.Metadata.Name == "" {
		return nil, fmt.Errorf("invalid behavior identity/type")
	}
	v, err := configurationValue(b.Configuration)
	if err != nil {
		return nil, err
	}
	f := hclwrite.NewEmptyFile()
	body := f.Body().AppendNewBlock("resource", []string{"hue_behavior_instance", name}).Body()
	body.SetAttributeValue("name", cty.StringVal(b.Metadata.Name))
	body.SetAttributeValue("enabled", cty.BoolVal(b.Enabled))
	body.SetAttributeRaw("configuration", hclwrite.TokensForFunctionCall("jsonencode", hclwrite.TokensForValue(v)))
	return f.Bytes(), nil
}

// PrepareBehavior accepts jsonencode of a literal object only. References and
// comments are never replaced with bridge UUIDs or silently discarded.
func PrepareBehavior(dir, name string, baseline Baseline, b hue.BehaviorInstance) (*Change, error) {
	if b.ID != baseline.ID || b.Type != "behavior_instance" || b.ScriptID != baseline.ScriptID {
		return nil, fmt.Errorf("behavior identity/script differs from state")
	}
	c, block, err := findResource(dir, "behavior_instance", name)
	if err != nil {
		return nil, err
	}
	for _, field := range []struct {
		key         string
		old, remote cty.Value
	}{
		{"name", cty.StringVal(baseline.Name), cty.StringVal(b.Metadata.Name)},
		{"enabled", cty.BoolVal(baseline.Enabled), cty.BoolVal(b.Enabled)},
	} {
		a := block.Body.Attributes[field.key]
		if a == nil {
			return nil, fmt.Errorf("%s must be explicitly configured", field.key)
		}
		if !behaviorLiteral(a.Expr) {
			return nil, fmt.Errorf("%s must be a literal; expressions are not overwritten", field.key)
		}
		local, d := a.Expr.Value(nil)
		if d.HasErrors() || !local.IsWhollyKnown() || local.IsNull() {
			return nil, fmt.Errorf("%s must be a literal", field.key)
		}
		if local.RawEquals(field.remote) {
			continue
		}
		if !local.RawEquals(field.old) {
			return nil, fmt.Errorf("local %s differs from state; resolve the local edit first", field.key)
		}
		c.valueEdit(a.Expr, field.remote, field.key)
	}
	a := block.Body.Attributes["configuration"]
	if a == nil {
		return nil, fmt.Errorf("configuration must be explicitly configured")
	}
	call, ok := a.Expr.(*hclsyntax.FunctionCallExpr)
	if !ok || call.Name != "jsonencode" || len(call.Args) != 1 || call.ExpandFinal {
		return nil, fmt.Errorf("configuration must use jsonencode({...}) with a literal object")
	}
	if !behaviorLiteral(call.Args[0]) {
		return nil, fmt.Errorf("configuration contains expressions; resource references are not overwritten")
	}
	local, d := call.Args[0].Value(nil)
	if d.HasErrors() || !local.IsWhollyKnown() || !local.Type().IsObjectType() {
		return nil, fmt.Errorf("configuration contains expressions; resource references are not overwritten")
	}
	remote, err := configurationValue(b.Configuration)
	if err != nil {
		return nil, err
	}
	if !local.RawEquals(remote) {
		old, err := configurationValue([]byte(baseline.Configuration))
		if err != nil {
			return nil, err
		}
		if !local.RawEquals(old) {
			return nil, fmt.Errorf("local configuration differs from state; resolve the local edit first")
		}
		r := call.Args[0].Range()
		tokens, d := hclsyntax.LexExpression(r.SliceBytes(c.Original), c.Path, r.Start)
		if d.HasErrors() {
			return nil, fmt.Errorf("cannot parse configuration")
		}
		for _, token := range tokens {
			if token.Type == hclsyntax.TokenComment {
				return nil, fmt.Errorf("configuration contains comments; review and update it manually")
			}
		}
		c.valueEdit(call.Args[0], remote, "configuration")
	}
	c.finish()
	return c, nil
}

func behaviorLiteral(expr hclsyntax.Expression) bool {
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.TemplateExpr:
		return e.IsStringLiteral()
	case *hclsyntax.UnaryOpExpr:
		v, ok := e.Val.(*hclsyntax.LiteralValueExpr)
		return ok && e.Op == hclsyntax.OpNegate && v.Val.Type() == cty.Number
	case *hclsyntax.TupleConsExpr:
		for _, item := range e.Exprs {
			if !behaviorLiteral(item) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		// object() also rejects duplicate keys instead of collapsing them.
		if _, err := object(e); err != nil {
			return false
		}
		for _, item := range e.Items {
			key, ok := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
			if !ok || key.ForceNonLiteral {
				return false
			}
			if t, ok := key.Wrapped.(*hclsyntax.ScopeTraversalExpr); ok {
				if len(t.Traversal) != 1 {
					return false
				}
			} else if !behaviorLiteral(key.Wrapped) {
				return false
			}
			if !behaviorLiteral(item.ValueExpr) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
