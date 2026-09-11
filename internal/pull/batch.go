package pull

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

type ManagedResource struct {
	Address, ID, Kind string
	Attributes        map[string]json.RawMessage
}

// ManagedResources rejects ambiguous state entries instead of treating them as new.
func ManagedResources(data []byte) ([]ManagedResource, error) {
	var s struct {
		Resources []struct {
			Mode, Type, Name, Module, Provider string
			Instances                          []struct {
				IndexKey   json.RawMessage `json:"index_key"`
				Deposed    string
				Attributes map[string]json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	var result []ManagedResource
	seen := map[string]bool{}
	for _, r := range s.Resources {
		if r.Mode != "managed" {
			continue
		}
		address := r.Type + "." + r.Name
		if r.Module != "" {
			address = r.Module + "." + address
		}
		kind, _, err := ResourceAddress(address)
		if !strings.HasPrefix(r.Type, "hue_") {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", address, err)
		}
		baseline, err := State(data, address)
		if err != nil {
			return nil, err
		}
		if seen[baseline.ID] {
			return nil, fmt.Errorf("UUID %s is managed at multiple addresses", baseline.ID)
		}
		seen[baseline.ID] = true
		result = append(result, ManagedResource{Address: address, ID: baseline.ID, Kind: kind, Attributes: r.Instances[0].Attributes})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, nil
}

// RemoteDefinition projects supported writable attributes, excluding runtime fields.
func RemoteDefinition(raw json.RawMessage, kind, name string, groups map[string]string) ([]byte, error) {
	switch kind {
	case "scene":
		return NewScene(raw, name, groups)
	case "behavior_instance":
		return NewBehavior(raw, name)
	default:
		return NewGroup(raw, kind, name)
	}
}

func ResourceName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	if name == "" {
		return "resource"
	}
	if !hclsyntax.ValidIdentifier(name) {
		name = "resource_" + name
	}
	return name
}

func decodeValue(raw []byte) (cty.Value, error) {
	if len(raw) == 0 {
		return cty.NilVal, nil
	}
	ty, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(raw, ty)
}

// StateContext resolves direct resource references within the same module.
// Variables, outputs and arbitrary functions are deliberately not guessed.
func StateContext(resources []ManagedResource, scope string) *hcl.EvalContext {
	byType := map[string]map[string]cty.Value{}
	for _, r := range resources {
		if ModuleAddress(r.Address) != scope {
			continue
		}
		_, name, _ := ResourceAddress(r.Address)
		raw, _ := json.Marshal(r.Attributes)
		v, err := decodeValue(raw)
		if err != nil {
			continue
		}
		typ := "hue_" + r.Kind
		if byType[typ] == nil {
			byType[typ] = map[string]cty.Value{}
		}
		byType[typ][name] = v
	}
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{}, Functions: map[string]function.Function{"jsonencode": stdlib.JSONEncodeFunc}}
	for typ, values := range byType {
		ctx.Variables[typ] = cty.ObjectVal(values)
	}
	return ctx
}

func definitionAttributes(src []byte) (map[string]hclsyntax.Expression, error) {
	f, d := hclsyntax.ParseConfig(src, "generated.tf", hcl.InitialPos)
	if d.HasErrors() {
		return nil, fmt.Errorf("invalid generated definition")
	}
	attrs := map[string]hclsyntax.Expression{}
	for _, b := range f.Body.(*hclsyntax.Body).Blocks {
		if b.Type == "resource" {
			for k, a := range b.Body.Attributes {
				attrs[k] = a.Expr
			}
		}
	}
	return attrs, nil
}

// CanonicalAttributes is the persistent three-way merge ancestor. It is separate
// from Terraform state, which a normal plan/apply can refresh independently.
func CanonicalAttributes(src []byte) (map[string]json.RawMessage, error) {
	attrs, err := definitionAttributes(src)
	if err != nil {
		return nil, err
	}
	result := map[string]json.RawMessage{}
	ctx := StateContext(nil, "")
	for k, e := range attrs {
		v, d := e.Value(ctx)
		if d.HasErrors() {
			return nil, fmt.Errorf("cannot evaluate generated %s", k)
		}
		raw, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return nil, err
		}
		result[k] = raw
	}
	return result, nil
}

// PrepareSync merges at literal leaves. Reference expressions and comments remain
// intact; edits that would overwrite expressions or remove comments are blocked.
func PrepareSync(dir, kind, name string, old map[string]json.RawMessage, remote []byte, ctx *hcl.EvalContext) (*Change, error) {
	c, b, err := findResource(dir, kind, name)
	if err != nil {
		return nil, err
	}
	attrs, err := definitionAttributes(remote)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for k := range attrs {
		keys[k] = true
	}
	for k := range old {
		if writableField(kind, k) {
			keys[k] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		ancestor, err := decodeValue(old[k])
		if err != nil {
			return nil, err
		}
		target := cty.NilVal
		if e := attrs[k]; e != nil {
			var d hcl.Diagnostics
			target, d = e.Value(StateContext(nil, ""))
			if d.HasErrors() {
				return nil, fmt.Errorf("invalid remote %s", k)
			}
		}
		a := b.Body.Attributes[k]
		if a == nil {
			// Omitted optional computed fields need no declaration unless the bridge changed them.
			if equalValue(ancestor, target, k) || absent(target) {
				continue
			}
			if !absent(ancestor) {
				return nil, fmt.Errorf("conflict at %s: attribute removed locally and changed on bridge", k)
			}
			at := b.CloseBraceRange.Start.Byte
			c.Edits = append(c.Edits, Edit{Start: at, End: at, After: "  " + k + " = " + string(hclwrite.TokensForValue(target).Bytes()) + "\n", Field: k})
			continue
		}
		if absent(target) {
			local, d := a.Expr.Value(ctx)
			if d.HasErrors() || !equalValue(local, ancestor, k) {
				return nil, fmt.Errorf("conflict at %s: removed on bridge and changed or unresolved locally", k)
			}
			if err := c.removeRange(a.Range(), k); err != nil {
				return nil, err
			}
			continue
		}
		if kind == "scene" && k == "actions" {
			ancestor, target, err = actionRepresentations(a.Expr, ancestor, target)
			if err != nil {
				return nil, err
			}
		}
		if err := c.mergeExpr(a.Expr, ancestor, target, ctx, k); err != nil {
			return nil, err
		}
	}
	c.finish()
	return c, nil
}

func writableField(kind, k string) bool {
	fields := map[string]string{"room": " name archetype children ", "zone": " name archetype children ", "scene": " name group actions speed auto_dynamic image_id ", "behavior_instance": " name enabled script_id configuration "}
	return strings.Contains(fields[kind], " "+k+" ")
}
func absent(v cty.Value) bool { return v == cty.NilVal || v.IsNull() }
func equalValue(a, b cty.Value, field string) bool {
	if absent(a) || absent(b) {
		return absent(a) && absent(b)
	}
	if a.Type() == cty.String && b.Type() == cty.String && (field == "configuration" || strings.HasSuffix(field, ".gradient") || strings.HasSuffix(field, ".effects")) {
		av, ae := configurationValue([]byte(a.AsString()))
		bv, be := configurationValue([]byte(b.AsString()))
		if ae == nil && be == nil {
			return av.RawEquals(bv)
		}
	}
	// Terraform uses sets for children and pads optional object fields with nulls.
	if field == "children" {
		a = sortStrings(a)
		b = sortStrings(b)
	}
	if (a.Type().IsObjectType() || a.Type().IsMapType()) && (b.Type().IsObjectType() || b.Type().IsMapType()) {
		am, bm := a.AsValueMap(), b.AsValueMap()
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for k := range keys {
			if !equalValue(am[k], bm[k], field+"."+k) {
				return false
			}
		}
		return true
	}
	if a.Type() == cty.Number && b.Type() == cty.Number && (strings.HasSuffix(field, "color_xy.x") || strings.HasSuffix(field, "color_xy.y")) {
		return equalScalar(a, b, "color_xy.x")
	}
	return a.RawEquals(b)
}
func sortStrings(v cty.Value) cty.Value {
	if !(v.Type().IsTupleType() || v.Type().IsListType() || v.Type().IsSetType()) {
		return v
	}
	ss := []string{}
	for _, x := range v.AsValueSlice() {
		if x.Type() != cty.String {
			return v
		}
		ss = append(ss, x.AsString())
	}
	sort.Strings(ss)
	xs := []cty.Value{}
	for _, s := range ss {
		xs = append(xs, cty.StringVal(s))
	}
	return cty.TupleVal(xs)
}
func objectChild(v cty.Value, k string) cty.Value {
	if absent(v) || !(v.Type().IsObjectType() || v.Type().IsMapType()) {
		return cty.NilVal
	}
	return v.AsValueMap()[k]
}

func (c *Change) mergeExpr(expr hclsyntax.Expression, old, remote cty.Value, ctx *hcl.EvalContext, field string) error {
	if equalValue(old, remote, field) {
		return nil
	} // keep local-only edits
	local, d := expr.Value(ctx)
	if !d.HasErrors() && equalValue(local, remote, field) {
		return nil
	}
	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "jsonencode" && len(call.Args) == 1 && !call.ExpandFinal {
		if old.Type() != cty.String || remote.Type() != cty.String {
			return fmt.Errorf("conflict at %s: expected JSON strings", field)
		}
		ov, err := decodeValue([]byte(old.AsString()))
		if err != nil {
			return err
		}
		rv, err := decodeValue([]byte(remote.AsString()))
		if err != nil {
			return err
		}
		return c.mergeExpr(call.Args[0], ov, rv, ctx, field)
	}
	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok && (remote.Type().IsObjectType() || remote.Type().IsMapType()) {
		items, err := object(obj)
		if err != nil {
			return err
		}
		keys := map[string]bool{}
		for k := range items {
			keys[k] = true
		}
		for k := range remote.AsValueMap() {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			ov, rv := objectChild(old, k), objectChild(remote, k)
			e := items[k]
			path := field + "." + k
			if e == nil {
				if equalValue(ov, rv, path) || absent(rv) {
					continue
				}
				if !absent(ov) {
					return fmt.Errorf("conflict at %s: removed locally and changed on bridge", path)
				}
				at := obj.Range().End.Byte - 1
				c.Edits = append(c.Edits, Edit{Start: at, End: at, After: "\n" + string(hclwrite.TokensForValue(cty.StringVal(k)).Bytes()) + " = " + string(syncValueTokens(rv, path).Bytes()) + "\n", Field: path})
				continue
			}
			if absent(rv) {
				if absent(ov) {
					continue
				} // local-only addition
				lv, d := e.Value(ctx)
				if d.HasErrors() || !equalValue(lv, ov, path) {
					return fmt.Errorf("conflict at %s: removed on bridge and changed locally", path)
				}
				for _, item := range obj.Items {
					if item.ValueExpr == e {
						r := hcl.RangeBetween(item.KeyExpr.Range(), e.Range())
						if err := c.removeObjectItem(r, obj.Range(), path); err != nil {
							return err
						}
					}
				}
				continue
			}
			if err := c.mergeExpr(e, ov, rv, ctx, path); err != nil {
				return err
			}
		}
		return nil
	}
	if seq, ok := expr.(*hclsyntax.TupleConsExpr); ok && field != "children" && remote.Type().IsTupleType() && !absent(old) && old.Type().IsTupleType() {
		return c.mergeTuple(seq, old, remote, ctx, field)
	}

	if d.HasErrors() || !equalValue(local, old, field) {
		return mergeConflict(field, old, local, remote)
	}
	if traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr); ok {
		if replacement := matchingReference(traversal, remote, ctx); replacement != "" {
			r := expr.Range()
			c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), After: replacement, Field: field})
			return nil
		}
	}
	if !behaviorLiteral(expr) {
		return fmt.Errorf("%s: expression cannot be replaced; update its source", field)
	}
	if err := c.noComments(expr.Range()); err != nil {
		return err
	}
	c.valueEdit(expr, remote, field)
	return nil
}
func (c *Change) noComments(r hcl.Range) error {
	ts, d := hclsyntax.LexExpression(r.SliceBytes(c.Original), c.Path, r.Start)
	if d.HasErrors() {
		return fmt.Errorf("cannot inspect edit")
	}
	for _, t := range ts {
		if t.Type == hclsyntax.TokenComment {
			return fmt.Errorf("edit would remove a comment in %s", c.Path)
		}
	}
	return nil
}
func (c *Change) removeRange(r hcl.Range, field string) error {
	if err := c.noComments(r); err != nil {
		return err
	}
	c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), Field: field})
	return nil
}
func (c *Change) removeObjectItem(r, container hcl.Range, field string) error {
	end := r.End.Byte
	for end < container.End.Byte && (c.Original[end] == ' ' || c.Original[end] == '\t') {
		end++
	}
	if end < container.End.Byte && c.Original[end] == ',' {
		r.End.Byte = end + 1
	}
	return c.removeRange(r, field)
}

func PrepareRemoval(dir, kind, name string, old map[string]json.RawMessage, ctx *hcl.EvalContext) (*Change, error) {
	c, b, err := findResource(dir, kind, name)
	if err != nil {
		return nil, err
	}
	for k, a := range b.Body.Attributes {
		if !writableField(kind, k) {
			continue
		}
		v, d := a.Expr.Value(ctx)
		ov, err := decodeValue(old[k])
		if err != nil {
			return nil, err
		}
		if kind == "scene" && k == "actions" {
			ov, _, err = actionRepresentations(a.Expr, ov, ov)
			if err != nil {
				return nil, err
			}
		}
		if d.HasErrors() || !equalValue(v, ov, k) {
			return nil, fmt.Errorf("conflict at %s: deleted on bridge but changed or unresolved locally", k)
		}
	}
	// Keep comments inside deleted resources in the backup; surrounding comments survive.
	r := b.Range()
	c.Edits = []Edit{{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), Field: "resource"}}
	c.finish()
	return c, nil
}

// CombineChanges prevents edits to different resources in the same file from
// overwriting one another. It also validates the final HCL before any write.
func CombineChanges(changes []*Change) ([]*Change, error) {
	byPath := map[string]*Change{}
	for _, c := range changes {
		p := byPath[c.Path]
		if p == nil {
			p = &Change{Path: c.Path, Original: c.Original}
			byPath[c.Path] = p
		}
		if !bytes.Equal(p.Original, c.Original) {
			return nil, fmt.Errorf("inconsistent file snapshot")
		}
		p.Edits = append(p.Edits, c.Edits...)
	}
	result := []*Change{}
	for _, c := range byPath {
		c.finish()
		for i := 1; i < len(c.Edits); i++ {
			if c.Edits[i].Start < c.Edits[i-1].End {
				return nil, fmt.Errorf("overlapping edits in %s", c.Path)
			}
		}
		_, d := hclsyntax.ParseConfig(c.Updated, c.Path, hcl.InitialPos)
		if d.HasErrors() {
			return nil, fmt.Errorf("proposed edits produce invalid HCL in %s", c.Path)
		}
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// ConfigModules follows declarations, using ModuleDir's existing source guards.
func ConfigModules(root string) (map[string]string, error) {
	result := map[string]string{}
	var visit func(string) error
	visit = func(scope string) error {
		prefix := scope
		if prefix != "" {
			prefix += "."
		}
		dir, err := ModuleDir(root, prefix+"hue_room.placeholder")
		if err != nil {
			return err
		}
		result[scope] = dir
		files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			return err
		}
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, d := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
			if d.HasErrors() {
				return fmt.Errorf("invalid %s", path)
			}
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				if b.Type == "module" {
					if err := visit(prefix + "module." + b.Labels[0]); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return result, visit("")
}

// CheckRemovals checks references against the proposed configuration, so edits
// removing a switch's reference and its scene can be synchronized together.
func CheckRemovals(modules map[string]string, changes []*Change, removed []ManagedResource) ([]*Change, error) {
	updated := map[string][]byte{}
	for _, c := range changes {
		updated[c.Path] = c.Updated
	}
	var importChanges []*Change
	for scope, dir := range modules {
		files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			return nil, err
		}
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			proposed := src
			if v, ok := updated[path]; ok {
				proposed = v
			}
			f, d := hclsyntax.ParseConfig(proposed, path, hcl.InitialPos)
			if d.HasErrors() {
				return nil, fmt.Errorf("cannot inspect references in %s", path)
			}
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				// Applied declarative imports are safe to remove with their resource.
				if b.Type == "import" && scope == "" {
					if to := b.Body.Attributes["to"]; to != nil {
						addr := string(to.Expr.Range().SliceBytes(proposed))
						matched := false
						for _, r := range removed {
							if addr == r.Address {
								matched = true
							}
						}
						if matched {
							if !bytes.Equal(src, proposed) {
								return nil, fmt.Errorf("remove obsolete import block in %s before pulling deletions", path)
							}
							c := &Change{Path: path, Original: src}
							br := b.Range()
							c.Edits = []Edit{{Start: br.Start.Byte, End: br.End.Byte, Field: "import"}}
							c.finish()
							importChanges = append(importChanges, c)
							continue
						}
					}
				}
				var conflict error
				hclsyntax.VisitAll(b, func(node hclsyntax.Node) hcl.Diagnostics {
					expr, ok := node.(*hclsyntax.ScopeTraversalExpr)
					if !ok {
						return nil
					}
					tr := expr.Traversal
					if len(tr) < 2 {
						return nil
					}
					root := tr.RootName()
					attr, ok := tr[1].(hcl.TraverseAttr)
					if !ok {
						return nil
					}
					candidate := root + "." + attr.Name
					if scope != "" {
						candidate = scope + "." + candidate
					}
					for _, r := range removed {
						if candidate == r.Address || (root == "module" && strings.HasPrefix(r.Address, candidate+".")) {
							conflict = fmt.Errorf("%s references deleted %s; update that reference before pulling", path, r.Address)
						}
					}
					return nil
				})
				if conflict != nil {
					return nil, conflict
				}
			}
		}
	}
	return importChanges, nil
}

// Terraform state includes computed aliases for scene colors. Compare in the
// representation used by the source instead of mistaking those aliases for edits.
func actionRepresentations(expr hclsyntax.Expression, old, remote cty.Value) (cty.Value, cty.Value, error) {
	locals, err := object(expr)
	if err != nil {
		return old, remote, nil
	}
	adapt := func(v cty.Value) (cty.Value, error) {
		if absent(v) || !v.Type().IsObjectType() {
			return v, nil
		}
		actions := v.AsValueMap()
		for id, action := range actions {
			if absent(action) || !action.Type().IsObjectType() {
				continue
			}
			fields, err := object(locals[id])
			if err != nil {
				continue
			}
			if fields["color_hex"] != nil {
				return cty.NilVal, fmt.Errorf("%s: color_hex cannot be reconstructed losslessly; use color_xy", id)
			}
			if fields["kelvin"] != nil && fields["mirek"] != nil {
				return cty.NilVal, fmt.Errorf("%s: both kelvin and mirek are configured", id)
			}
			values := action.AsValueMap()
			delete(values, "color_hex")
			if fields["kelvin"] == nil {
				delete(values, "kelvin")
			} else {
				mirek := values["mirek"]
				if !absent(mirek) && mirek.Type() == cty.Number {
					n, _ := mirek.AsBigFloat().Int64()
					kelvin := cty.NumberIntVal(color.MirekToKelvin(n))
					local, d := fields["kelvin"].Value(nil)
					if !d.HasErrors() && !local.IsNull() && local.Type() == cty.Number {
						k, _ := local.AsBigFloat().Int64()
						if color.EqualMirek(color.KelvinToMirek(k), n, 153, 500) {
							kelvin = local
						}
					}
					values["kelvin"] = kelvin
				}
				delete(values, "mirek")
			}
			actions[id] = cty.ObjectVal(values)
		}
		return cty.ObjectVal(actions), nil
	}
	old, err = adapt(old)
	if err != nil {
		return old, remote, err
	}
	remote, err = adapt(remote)
	return old, remote, err
}

func mergeConflict(field string, old, local, remote cty.Value) error {
	render := func(v cty.Value) string {
		if v == cty.NilVal {
			return "<absent or unresolved>"
		}
		if !v.IsWhollyKnown() {
			return "<unknown>"
		}
		return string(hclwrite.TokensForValue(v).Bytes())
	}
	return fmt.Errorf("conflict at %s: base=%s, local=%s, bridge=%s", field, render(old), render(local), render(remote))
}
func matchingReference(expr *hclsyntax.ScopeTraversalExpr, remote cty.Value, ctx *hcl.EvalContext) string {
	tr := expr.Traversal
	if len(tr) != 3 || absent(remote) || remote.Type() != cty.String {
		return ""
	}
	last, ok := tr[2].(hcl.TraverseAttr)
	if !ok || last.Name != "id" {
		return ""
	}
	typ := tr.RootName()
	if !strings.HasPrefix(typ, "hue_") {
		return ""
	}
	values := ctx.Variables[typ]
	if absent(values) || !values.Type().IsObjectType() {
		return ""
	}
	found := ""
	for name, v := range values.AsValueMap() {
		if equalValue(objectChild(v, "id"), remote, "id") {
			if found != "" {
				return ""
			}
			found = typ + "." + name + ".id"
		}
	}
	return found
}

// CheckProviderEnvironment prevents the CLI reader and Terraform importer from
// silently using different bridges. Provider expressions are not evaluated here.
func CheckProviderEnvironment(modules map[string]string, host, key string) error {
	for _, dir := range modules {
		paths, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, d := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
			if d.HasErrors() {
				return fmt.Errorf("cannot parse provider configuration")
			}
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				if b.Type != "provider" || len(b.Labels) != 1 || b.Labels[0] != "hue" {
					continue
				}
				for name, want := range map[string]string{"host": host, "application_key": key} {
					a := b.Body.Attributes[name]
					if a == nil {
						continue
					}
					v, d := a.Expr.Value(nil)
					if d.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String || v.AsString() != want {
						return fmt.Errorf("provider %s in %s cannot be matched to the HUE_BRIDGE environment; use matching literal values or environment defaults", name, path)
					}
				}
			}
		}
	}
	return nil
}

func (c *Change) mergeTuple(seq *hclsyntax.TupleConsExpr, old, remote cty.Value, ctx *hcl.EvalContext, field string) error {
	ov, rv := old.AsValueSlice(), remote.AsValueSlice()
	if len(seq.Exprs) != len(ov) {
		lv, _ := seq.Value(ctx)
		return mergeConflict(field, old, lv, remote)
	}
	// Preserve unchanged prefixes/suffixes, including their references and comments.
	prefix := 0
	for prefix < len(ov) && prefix < len(rv) && equalValue(ov[prefix], rv[prefix], field) {
		prefix++
	}
	suffix := 0
	for suffix < len(ov)-prefix && suffix < len(rv)-prefix && equalValue(ov[len(ov)-1-suffix], rv[len(rv)-1-suffix], field) {
		suffix++
	}
	oldEnd, newEnd := len(ov)-suffix, len(rv)-suffix
	paired := oldEnd - prefix
	if newEnd-prefix < paired {
		paired = newEnd - prefix
	}
	for i := 0; i < paired; i++ {
		index := prefix + i
		if err := c.mergeExpr(seq.Exprs[index], ov[index], rv[index], ctx, fmt.Sprintf("%s[%d]", field, index)); err != nil {
			return err
		}
	}
	for index := prefix + paired; index < oldEnd; index++ {
		e := seq.Exprs[index]
		lv, d := e.Value(ctx)
		if d.HasErrors() || !equalValue(lv, ov[index], field) {
			return mergeConflict(fmt.Sprintf("%s[%d]", field, index), ov[index], lv, cty.NilVal)
		}
		if err := c.removeObjectItem(e.Range(), seq.Range(), field); err != nil {
			return err
		}
	}
	if prefix+paired < newEnd {
		at := seq.Range().End.Byte - 1
		previousEnd := seq.Range().Start.Byte + 1
		if oldEnd < len(seq.Exprs) {
			at = seq.Exprs[oldEnd].Range().Start.Byte
		}
		if oldEnd > 0 {
			previousEnd = seq.Exprs[oldEnd-1].Range().End.Byte
		}
		separator := ""
		if oldEnd > 0 {
			tokens, d := hclsyntax.LexExpression(c.Original[previousEnd:at], c.Path, hcl.InitialPos)
			if d.HasErrors() {
				return fmt.Errorf("cannot inspect tuple separator")
			}
			hasComma := false
			for _, token := range tokens {
				if token.Type == hclsyntax.TokenComma {
					hasComma = true
				}
			}
			if !hasComma {
				separator = ","
			}
		}
		var text strings.Builder
		text.WriteString(separator + "\n")
		for index := prefix + paired; index < newEnd; index++ {
			text.Write(hclwrite.TokensForValue(rv[index]).Bytes())
			text.WriteString(",\n")
		}
		c.Edits = append(c.Edits, Edit{Start: at, End: at, After: text.String(), Field: field})
	}
	return nil
}

func syncValueTokens(v cty.Value, field string) hclwrite.Tokens {
	if v.Type() == cty.String && (strings.HasSuffix(field, ".gradient") || strings.HasSuffix(field, ".effects")) {
		obj, err := configurationValue([]byte(v.AsString()))
		if err == nil {
			return hclwrite.TokensForFunctionCall("jsonencode", hclwrite.TokensForValue(obj))
		}
	}
	return hclwrite.TokensForValue(v)
}
