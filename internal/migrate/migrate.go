// Package migrate rewrites configurations for provider 0.1 into the forms that
// provider 0.2.0 takes. It edits the syntax of each file, so that the
// expressions and the formatting around what it rewrites stay as they are.
package migrate

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// A Note tells about something in a file that migrate left for the user to
// rewrite, or rewrote in a way that the user may want to check.
type Note struct {
	Range   hcl.Range
	Address string
	Text    string
	// Left is true when migrate did not rewrite it.
	Left bool
}

func (n Note) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", n.Range.Filename, n.Range.Start.Line, n.Address, n.Text)
}

// A Module is a module call whose source is a local directory.
type Module struct {
	Name, Source string
	Range        hcl.Range
}

// Result is a file as migrate rewrote it.
type Result struct {
	Output  []byte
	Notes   []Note
	Modules []Module
}

// File rewrites the hue_scene resources in one file in the native syntax.
func File(filename string, src []byte) (Result, error) {
	file, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return Result{}, diags
	}
	r := rewriter{src: src, seen: map[string]bool{}}
	var modules []Module
	for _, block := range file.Body.(*hclsyntax.Body).Blocks {
		switch {
		case block.Type == "resource" && len(block.Labels) == 2 && block.Labels[0] == "hue_scene":
			r.scene("hue_scene."+block.Labels[1], block.Body)
		case block.Type == "module" && len(block.Labels) == 1:
			if a, ok := block.Body.Attributes["source"]; ok {
				if v, d := a.Expr.Value(nil); !d.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
					if s := v.AsString(); strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
						modules = append(modules, Module{Name: block.Labels[0], Source: s, Range: a.SrcRange})
					}
				}
			}
		}
	}
	out, err := r.apply()
	if err != nil {
		return Result{}, err
	}
	sort.SliceStable(r.notes, func(i, j int) bool { return r.notes[i].Range.Start.Byte < r.notes[j].Range.Start.Byte })
	// A file that terraform fmt leaves as it is stays that way.
	if len(r.edits) > 0 && bytes.Equal(hclwrite.Format(src), src) {
		out = hclwrite.Format(out)
	}
	return Result{Output: out, Notes: r.notes, Modules: modules}, nil
}

type edit struct {
	start, end int
	text       string
}

type rewriter struct {
	src   []byte
	edits []edit
	notes []Note
	// seen avoids repeating a note about the same expression.
	seen map[string]bool
}

func (r *rewriter) text(e hclsyntax.Expression) string {
	rng := e.Range()
	return string(r.src[rng.Start.Byte:rng.End.Byte])
}

// brief returns the start of an expression for a note: its first line, and
// at most 60 characters of it.
func (r *rewriter) brief(e hclsyntax.Expression) string {
	text := r.text(e)
	cut := false
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text, cut = strings.TrimSpace(text[:i]), true
	}
	if runes := []rune(text); len(runes) > 60 {
		text, cut = string(runes[:57]), true
	}
	if cut {
		text += "..."
	}
	return text
}

func (r *rewriter) note(address string, rng hcl.Range, left bool, text string) {
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	key := fmt.Sprintf("%d:%s", rng.Start.Byte, text)
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.notes = append(r.notes, Note{Range: rng, Address: address, Text: text, Left: left})
}

func (r *rewriter) replace(rng hcl.Range, text string) {
	r.edits = append(r.edits, edit{rng.Start.Byte, rng.End.Byte, text})
}

// indent returns the indentation of the line that holds offset.
func (r *rewriter) indent(offset int) string {
	start := bytes.LastIndexByte(r.src[:offset], '\n') + 1
	end := start
	for end < len(r.src) && (r.src[end] == ' ' || r.src[end] == '\t') {
		end++
	}
	return string(r.src[start:end])
}

func (r *rewriter) apply() ([]byte, error) {
	sort.Slice(r.edits, func(i, j int) bool { return r.edits[i].start < r.edits[j].start })
	var out bytes.Buffer
	at := 0
	for _, e := range r.edits {
		if e.start < at {
			return nil, errors.New("migrate: overlapping edits")
		}
		out.Write(r.src[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(r.src[at:])
	return out.Bytes(), nil
}

func (r *rewriter) scene(address string, body *hclsyntax.Body) {
	if a, ok := body.Attributes["palette"]; ok {
		r.palette(address, a)
	}
	if a, ok := body.Attributes["actions"]; ok {
		r.actions(address, a.Expr)
	}
}

// jsonencodeArg returns the argument of jsonencode(...).
func jsonencodeArg(e hclsyntax.Expression) (hclsyntax.Expression, bool) {
	call, ok := e.(*hclsyntax.FunctionCallExpr)
	if !ok || call.Name != "jsonencode" || len(call.Args) != 1 || call.ExpandFinal {
		return nil, false
	}
	return call.Args[0], true
}

// unread notes an attribute value that migrate cannot see into. An object or
// null is already in the 0.2.0 form.
func (r *rewriter) unread(address, name string, e hclsyntax.Expression) {
	switch e := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		return
	case *hclsyntax.LiteralValueExpr:
		if e.Val.IsNull() {
			return
		}
	}
	r.note(address, e.Range(), true, fmt.Sprintf("%s is %s, which migrate does not read. If it is JSON, write it as an object.", name, r.brief(e)))
}

func (r *rewriter) palette(address string, a *hclsyntax.Attribute) {
	arg, ok := jsonencodeArg(a.Expr)
	if !ok {
		r.unread(address, "palette", a.Expr)
		return
	}
	c := converter{r: r}
	n, err := c.palette(arg)
	if err != nil {
		r.note(address, a.Expr.Range(), true, "palette was not rewritten: "+err.Error())
		return
	}
	if n == nil {
		r.removeAttribute(a)
		r.note(address, a.SrcRange, false, "palette had no entries, so migrate removed it. Terraform now leaves the palette to the bridge, and keeps a palette made in the Hue app. Write palette = {} to keep it empty.")
		return
	}
	r.commentsLost(address, a.Expr.Range(), "palette")
	r.replace(a.Expr.Range(), n.render(r.indent(a.SrcRange.Start.Byte)))
}

// removeAttribute removes the lines that hold an attribute.
func (r *rewriter) removeAttribute(a *hclsyntax.Attribute) {
	start := bytes.LastIndexByte(r.src[:a.SrcRange.Start.Byte], '\n') + 1
	end := a.SrcRange.End.Byte
	if i := bytes.IndexByte(r.src[end:], '\n'); i >= 0 {
		end += i + 1
	} else {
		end = len(r.src)
	}
	r.edits = append(r.edits, edit{start, end, ""})
}

// commentsLost notes comments inside a value that migrate rebuilt.
func (r *rewriter) commentsLost(address string, rng hcl.Range, name string) {
	tokens, _ := hclsyntax.LexExpression(r.src[rng.Start.Byte:rng.End.Byte], rng.Filename, rng.Start)
	for _, t := range tokens {
		if t.Type == hclsyntax.TokenComment {
			r.note(address, t.Range, false, fmt.Sprintf("Comments inside %s were not kept.", name))
			return
		}
	}
}

// actions walks the expression of actions, a map from light IDs to actions.
func (r *rewriter) actions(address string, e hclsyntax.Expression) {
	switch e := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			r.action(address, item.ValueExpr)
		}
	case *hclsyntax.ForExpr:
		r.action(address, e.ValExpr)
	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" {
			r.unreadActions(address, e)
			return
		}
		for _, arg := range e.Args {
			r.actions(address, arg)
		}
	case *hclsyntax.ConditionalExpr:
		r.actions(address, e.TrueResult)
		r.actions(address, e.FalseResult)
	case *hclsyntax.ParenthesesExpr:
		r.actions(address, e.Expression)
	default:
		r.unreadActions(address, e)
	}
}

func (r *rewriter) unreadActions(address string, e hclsyntax.Expression) {
	if lit, ok := e.(*hclsyntax.LiteralValueExpr); ok && lit.Val.IsNull() {
		return
	}
	r.note(address, e.Range(), true, fmt.Sprintf("actions use %s, which migrate does not read. Check it for kelvin, and for JSON in gradient, effects_v2 and dynamics.", r.brief(e)))
}

// action walks the expression of one action.
func (r *rewriter) action(address string, e hclsyntax.Expression) {
	switch e := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		r.actionObject(address, e)
	case *hclsyntax.FunctionCallExpr:
		switch e.Name {
		case "merge":
			for _, arg := range e.Args {
				r.action(address, arg)
			}
		case "provider::hue::color_from_hex":
		default:
			r.unreadActions(address, e)
		}
	case *hclsyntax.ConditionalExpr:
		r.action(address, e.TrueResult)
		r.action(address, e.FalseResult)
	case *hclsyntax.ParenthesesExpr:
		r.action(address, e.Expression)
	default:
		r.unreadActions(address, e)
	}
}

// key returns the name of an object item whose key is a name or a string.
func key(item hclsyntax.ObjectConsItem) (string, bool) {
	k, ok := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
	if !ok {
		return "", false
	}
	if name := hcl.ExprAsKeyword(k.Wrapped); name != "" && !k.ForceNonLiteral {
		return name, true
	}
	v, diags := k.Wrapped.Value(nil)
	if diags.HasErrors() || v.Type() != cty.String || !v.IsKnown() || v.IsNull() {
		return "", false
	}
	return v.AsString(), true
}

func isNull(e hclsyntax.Expression) bool {
	lit, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && lit.Val.IsNull()
}

func (r *rewriter) actionObject(address string, o *hclsyntax.ObjectConsExpr) {
	items := map[string]int{}
	for i, item := range o.Items {
		if k, ok := key(item); ok {
			items[k] = i
		}
	}
	r.kelvin(address, o, items)
	for _, name := range []string{"gradient", "effects_v2", "dynamics"} {
		i, ok := items[name]
		if !ok {
			continue
		}
		value := o.Items[i].ValueExpr
		arg, ok := jsonencodeArg(value)
		if !ok {
			r.unread(address, name, value)
			continue
		}
		c := converter{r: r}
		var n node
		var err error
		switch name {
		case "gradient":
			n, err = c.gradient(arg)
		case "effects_v2":
			n, err = c.effectsV2(arg)
		case "dynamics":
			n, err = c.dynamics(arg)
		}
		if err != nil {
			r.note(address, value.Range(), true, name+" was not rewritten: "+err.Error())
			continue
		}
		if name != "dynamics" {
			r.commentsLost(address, value.Range(), name)
		}
		r.replace(value.Range(), n.render(r.indent(value.Range().Start.Byte)))
	}
}

// removeItem removes an item from an object, with the comma or the line
// break that separates it from the next item, or from the one before.
func (r *rewriter) removeItem(o *hclsyntax.ObjectConsExpr, i int) {
	start, end := o.Items[i].KeyExpr.Range().Start.Byte, o.Items[i].ValueExpr.Range().End.Byte
	if i+1 < len(o.Items) {
		end = o.Items[i+1].KeyExpr.Range().Start.Byte
	} else if i > 0 {
		start = o.Items[i-1].ValueExpr.Range().End.Byte
	}
	r.edits = append(r.edits, edit{start, end, ""})
}

// kelvin rewrites kelvin into mirek. Configurations generated by 0.1 write
// both, or kelvin = null, and the mirek in them is what the bridge stores.
func (r *rewriter) kelvin(address string, o *hclsyntax.ObjectConsExpr, items map[string]int) {
	i, ok := items["kelvin"]
	if !ok {
		return
	}
	item := o.Items[i]
	if isNull(item.ValueExpr) {
		r.removeItem(o, i)
		return
	}
	j, hasMirek := items["mirek"]
	if hasMirek && !isNull(o.Items[j].ValueExpr) {
		r.removeItem(o, i)
		return
	}
	value := r.text(item.ValueExpr)
	mirek := "provider::hue::mirek_from_kelvin(" + value + ")"
	if lit, ok := item.ValueExpr.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.Number {
		k, accuracy := lit.Val.AsBigFloat().Int64()
		if accuracy != big.Exact || k <= 0 {
			r.note(address, item.ValueExpr.Range(), true, fmt.Sprintf("kelvin = %s was not rewritten: it is not a positive whole number.", value))
			return
		}
		if m := colors.MirekForKelvin(k); colors.KelvinToMirek(k) != m {
			mirek = strconv.FormatInt(m, 10)
			r.note(address, item.ValueExpr.Range(), false, fmt.Sprintf("kelvin = %d is outside the range of provider::hue::mirek_from_kelvin, so migrate wrote mirek = %d, the mirek that 0.1 sent for it.", k, m))
		}
	} else {
		r.note(address, item.ValueExpr.Range(), false, fmt.Sprintf("kelvin = %s became mirek = %s. The function fails at plan for a kelvin outside the API's range of 50 to 1000 mirek, which 0.1 brought into the range.", value, mirek))
	}
	// mirek = null, which configuration generated by 0.1 writes, gives way.
	if hasMirek {
		r.removeItem(o, j)
	}
	keyStart, valueStart := item.KeyExpr.Range().Start.Byte, item.ValueExpr.Range().Start.Byte
	separator := string(r.src[item.KeyExpr.Range().End.Byte:valueStart])
	r.edits = append(r.edits, edit{keyStart, item.ValueExpr.Range().End.Byte, "mirek" + separator + mirek})
}
