package migrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// A node is part of a value that migrate writes. Values that it does not
// restructure are copied from the source.
type node interface {
	// render returns the text of the node, for a value that starts on a line
	// indented by indent.
	render(indent string) string
}

// source is text copied from the configuration.
type source struct {
	text string
	// indent is the indentation of the line that the text started on.
	indent string
}

func (s source) render(indent string) string {
	lines := strings.Split(s.text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], s.indent) {
			lines[i] = indent + lines[i][len(s.indent):]
		}
	}
	return strings.Join(lines, "\n")
}

type object struct {
	items     []item
	multiline bool
}
type item struct {
	key   string
	value node
}

func (o object) render(indent string) string {
	if len(o.items) == 0 {
		return "{}"
	}
	if !o.multiline {
		parts := make([]string, len(o.items))
		for i, it := range o.items {
			parts[i] = it.key + " = " + it.value.render(indent)
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, it := range o.items {
		b.WriteString(indent + "  " + it.key + " = " + it.value.render(indent+"  ") + "\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

type tuple struct {
	elems []node
	// multiline puts each element on its own line. Otherwise the elements
	// follow each other, as in [{ ... }, { ... }].
	multiline bool
}

func (t tuple) render(indent string) string {
	if !t.multiline {
		parts := make([]string, len(t.elems))
		for i, e := range t.elems {
			parts[i] = e.render(indent)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	var b strings.Builder
	b.WriteString("[\n")
	for _, e := range t.elems {
		b.WriteString(indent + "  " + e.render(indent+"  ") + ",\n")
	}
	b.WriteString(indent + "]")
	return b.String()
}

// forList is a for expression that makes a list: [for v in coll : value].
type forList struct {
	head, tail string
	value      node
}

func (f forList) render(indent string) string {
	return f.head + " " + f.value.render(indent) + f.tail
}

// converter turns the argument of jsonencode, in the API's form, into the
// object that an attribute takes in 0.2.0. Wrappers that hold one field are
// flattened, as the provider's schema does.
type converter struct{ r *rewriter }

func (c converter) source(e hclsyntax.Expression) source {
	return source{text: c.r.text(e), indent: c.r.indent(e.Range().Start.Byte)}
}

func (c converter) multiline(e hclsyntax.Expression) bool {
	return strings.Contains(c.r.text(e), "\n")
}

// fields returns the items of an object in their order, and fails for a key
// that is not one of allowed.
func (c converter) fields(e hclsyntax.Expression, allowed ...string) ([]string, map[string]hclsyntax.Expression, error) {
	o, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not an object", c.r.text(e))
	}
	var order []string
	values := map[string]hclsyntax.Expression{}
	for _, it := range o.Items {
		k, ok := key(it)
		if !ok {
			return nil, nil, fmt.Errorf("the key %s is not a name", c.r.text(it.KeyExpr))
		}
		if !contains(allowed, k) {
			return nil, nil, fmt.Errorf("%s is not one of %s", k, strings.Join(allowed, ", "))
		}
		order = append(order, k)
		values[k] = it.ValueExpr
	}
	return order, values, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// inner returns the value of an object that holds only name, such as the xy
// of { xy = { x, y } }.
func (c converter) inner(e hclsyntax.Expression, name string) (hclsyntax.Expression, error) {
	order, values, err := c.fields(e, name)
	if err != nil {
		return nil, err
	}
	if len(order) != 1 {
		return nil, fmt.Errorf("%s has no %s", c.r.text(e), name)
	}
	return values[name], nil
}

// empty reports a list without entries, which the provider sends as one.
func empty(e hclsyntax.Expression) bool {
	if t, ok := e.(*hclsyntax.TupleConsExpr); ok {
		return len(t.Exprs) == 0
	}
	return isNull(e)
}

// list converts each entry of a list, written out or made by a for expression.
func (c converter) list(e hclsyntax.Expression, entry func(hclsyntax.Expression) (node, error)) (node, error) {
	switch e := e.(type) {
	case *hclsyntax.TupleConsExpr:
		t := tuple{}
		if len(e.Exprs) > 0 {
			between := c.r.src[e.OpenRange.End.Byte:e.Exprs[0].Range().Start.Byte]
			t.multiline = strings.Contains(string(between), "\n")
		}
		for _, x := range e.Exprs {
			n, err := entry(x)
			if err != nil {
				return nil, err
			}
			t.elems = append(t.elems, n)
		}
		return t, nil
	case *hclsyntax.ForExpr:
		if e.KeyExpr != nil || e.Group {
			return nil, fmt.Errorf("%s is not a list", c.r.text(e))
		}
		n, err := entry(e.ValExpr)
		if err != nil {
			return nil, err
		}
		head := "[for " + e.ValVar
		if e.KeyVar != "" {
			head = "[for " + e.KeyVar + ", " + e.ValVar
		}
		head += " in " + c.r.text(e.CollExpr) + " :"
		tail := "]"
		if e.CondExpr != nil {
			tail = " if " + c.r.text(e.CondExpr) + "]"
		}
		return forList{head: head, tail: tail, value: n}, nil
	}
	return nil, fmt.Errorf("%s is not a list that migrate can read", c.r.text(e))
}

// flat builds an object from the fields of e, which must have the required
// ones. Each field becomes the items that convert returns for it, in the order
// of the fields.
func (c converter) flat(e hclsyntax.Expression, allowed, required []string, convert func(name string, value hclsyntax.Expression) ([]item, error)) (object, error) {
	order, values, err := c.fields(e, allowed...)
	if err != nil {
		return object{}, err
	}
	for _, name := range required {
		if !contains(order, name) {
			return object{}, fmt.Errorf("%s has no %s", c.r.text(e), name)
		}
	}
	out := object{multiline: c.multiline(e)}
	for _, name := range order {
		items, err := convert(name, values[name])
		if err != nil {
			return object{}, err
		}
		out.items = append(out.items, items...)
	}
	return out, nil
}

// palette returns nil for a palette without entries.
func (c converter) palette(e hclsyntax.Expression) (node, error) {
	out, err := c.flat(e, []string{"color", "color_temperature", "dimming", "effects", "effects_v2"}, nil, func(name string, value hclsyntax.Expression) ([]item, error) {
		// A list without entries is sent empty when it is left out.
		if empty(value) {
			return nil, nil
		}
		var entry func(hclsyntax.Expression) (node, error)
		switch name {
		case "color":
			entry = c.paletteColor
		case "color_temperature":
			entry = c.paletteTemperature
		case "dimming":
			entry = func(e hclsyntax.Expression) (node, error) {
				return c.flat(e, []string{"brightness"}, []string{"brightness"}, c.copy)
			}
		case "effects_v2":
			entry = func(e hclsyntax.Expression) (node, error) {
				action, err := c.inner(e, "action")
				if err != nil {
					return nil, err
				}
				return c.effect(action)
			}
		case "effects":
			return nil, errors.New("it has entries in the deprecated effects list, which palette no longer takes. Move them to effects_v2 or remove them")
		}
		n, err := c.list(value, entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return []item{{name, n}}, nil
	})
	if err != nil {
		return nil, err
	}
	if len(out.items) == 0 {
		return nil, nil
	}
	return out, nil
}

// copy keeps a field as it is.
func (c converter) copy(name string, value hclsyntax.Expression) ([]item, error) {
	return []item{{name, c.source(value)}}, nil
}

// unwrap turns name = { field = value } into rename = value.
func (c converter) unwrap(field, rename string) func(string, hclsyntax.Expression) ([]item, error) {
	return func(_ string, value hclsyntax.Expression) ([]item, error) {
		v, err := c.inner(value, field)
		if err != nil {
			return nil, err
		}
		return []item{{rename, c.source(v)}}, nil
	}
}

// paletteColor turns { color = { xy }, dimming = { brightness } } into
// { color_xy, brightness }.
func (c converter) paletteColor(e hclsyntax.Expression) (node, error) {
	return c.flat(e, []string{"color", "dimming"}, []string{"color", "dimming"}, func(name string, value hclsyntax.Expression) ([]item, error) {
		if name == "color" {
			return c.unwrap("xy", "color_xy")(name, value)
		}
		return c.unwrap("brightness", "brightness")(name, value)
	})
}

// paletteTemperature turns { color_temperature = { mirek }, dimming = {
// brightness } } into { mirek, brightness }.
func (c converter) paletteTemperature(e hclsyntax.Expression) (node, error) {
	return c.flat(e, []string{"color_temperature", "dimming"}, []string{"color_temperature", "dimming"}, func(name string, value hclsyntax.Expression) ([]item, error) {
		if name == "color_temperature" {
			return c.unwrap("mirek", "mirek")(name, value)
		}
		return c.unwrap("brightness", "brightness")(name, value)
	})
}

// effect turns the action of effects_v2 into { effect, parameters }, with
// parameters { color_xy, mirek, speed }. Empty parameters are left out, as
// the provider reads them.
func (c converter) effect(e hclsyntax.Expression) (node, error) {
	return c.flat(e, []string{"effect", "parameters"}, []string{"effect"}, func(name string, value hclsyntax.Expression) ([]item, error) {
		if name == "effect" {
			return c.copy(name, value)
		}
		p, err := c.flat(value, []string{"color", "color_temperature", "speed"}, nil, func(name string, value hclsyntax.Expression) ([]item, error) {
			switch name {
			case "color":
				return c.unwrap("xy", "color_xy")(name, value)
			case "color_temperature":
				return c.unwrap("mirek", "mirek")(name, value)
			}
			return c.copy(name, value)
		})
		if err != nil || len(p.items) == 0 {
			return nil, err
		}
		return []item{{"parameters", p}}, nil
	})
}

// gradient turns { points = [{ color = { xy } }, ...], mode } into
// { points = [xy, ...], mode }.
func (c converter) gradient(e hclsyntax.Expression) (node, error) {
	return c.flat(e, []string{"points", "mode"}, []string{"points"}, func(name string, value hclsyntax.Expression) ([]item, error) {
		if name == "mode" {
			return c.copy(name, value)
		}
		points, err := c.list(value, func(point hclsyntax.Expression) (node, error) {
			color, err := c.inner(point, "color")
			if err != nil {
				return nil, err
			}
			xy, err := c.inner(color, "xy")
			if err != nil {
				return nil, err
			}
			return c.source(xy), nil
		})
		if err != nil {
			return nil, fmt.Errorf("points: %w", err)
		}
		return []item{{"points", points}}, nil
	})
}

// effectsV2 turns { action = { ... } } into the action.
func (c converter) effectsV2(e hclsyntax.Expression) (node, error) {
	action, err := c.inner(e, "action")
	if err != nil {
		return nil, err
	}
	return c.effect(action)
}

// dynamics keeps { duration } as it is written.
func (c converter) dynamics(e hclsyntax.Expression) (node, error) {
	if _, err := c.inner(e, "duration"); err != nil {
		return nil, err
	}
	return c.source(e), nil
}
