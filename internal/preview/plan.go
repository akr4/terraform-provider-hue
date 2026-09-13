// Package preview renders Terraform's evaluated plan without executing Terraform
// or contacting a bridge. Only explicitly supported lighting fields are exposed.
package preview

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

type Plan struct {
	FormatVersion    string  `json:"format_version"`
	TerraformVersion string  `json:"terraform_version"`
	PlannedValues    *Values `json:"planned_values"`
	Values           *Values `json:"values"`
	PriorState       *struct {
		Values *Values `json:"values"`
	} `json:"prior_state"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}
type Values struct {
	RootModule Module `json:"root_module"`
}
type Module struct {
	Resources []Resource `json:"resources"`
	Children  []Module   `json:"child_modules"`
}
type Resource struct {
	Address, Type, Name string
	Values              map[string]any `json:"values"`
	Sensitive           any            `json:"sensitive_values"`
}
type ResourceChange struct {
	Address, Type string
	Change        struct {
		Actions         []string
		Before, After   map[string]any
		Unknown         any `json:"after_unknown"`
		BeforeSensitive any `json:"before_sensitive"`
		AfterSensitive  any `json:"after_sensitive"`
	}
}
type View struct {
	Version, Source string
	Scenes          []Scene
}
type Scene struct {
	Address, Name, Group, Change string
	Rows                         []Row
	Palette                      []Row
	Changed                      bool
	Notice                       string
}
type Row struct {
	ID, Name      string
	Before, After Sample
	Changed       bool
}
type Sample struct {
	Exists                                     bool
	Text, Color, Brightness, On, Extra, Notice string
	R, G, B                                    int
	HasColor                                   bool
	CSS                                        string
}

func Decode(r io.Reader, inventory io.Reader) (View, error) {
	var p Plan
	dec := json.NewDecoder(r)
	if err := dec.Decode(&p); err != nil {
		return View{}, fmt.Errorf("read Terraform JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return View{}, fmt.Errorf("expected one Terraform JSON document")
	}
	if !strings.HasPrefix(p.FormatVersion, "1.") {
		return View{}, fmt.Errorf("unsupported Terraform JSON format %q; use terraform show -json", p.FormatVersion)
	}
	if p.PlannedValues == nil && p.Values == nil && p.ResourceChanges == nil {
		return View{}, fmt.Errorf("expected terraform show -json plan or state, not raw state or plan event stream")
	}
	names := map[string]string{}
	if inventory != nil {
		var data struct {
			Data []struct {
				ID, Type string
				Metadata struct{ Name string }
			}
		}
		if err := json.NewDecoder(inventory).Decode(&data); err != nil {
			return View{}, fmt.Errorf("read resource names: %w", err)
		}
		for _, r := range data.Data {
			if r.Type == "light" || r.Type == "room" || r.Type == "zone" {
				names[r.ID] = clean(r.Metadata.Name)
			}
		}
	}
	records := map[string]Resource{}
	before := map[string]Resource{}
	visit := func(v *Values, dst map[string]Resource) {
		if v == nil {
			return
		}
		var walk func(Module)
		walk = func(m Module) {
			for _, r := range m.Resources {
				r.Values = masked(r.Values, r.Sensitive)
				dst[r.Address] = r
				if id, ok := r.Values["id"].(string); ok {
					if n, ok := r.Values["name"].(string); ok && !special(n) {
						names[id] = clean(n)
					}
				}
			}
			for _, c := range m.Children {
				walk(c)
			}
		}
		walk(v.RootModule)
	}
	if p.PriorState != nil {
		visit(p.PriorState.Values, before)
	}
	visit(p.PlannedValues, records)
	source := "Plan: evaluated configuration"
	if p.PlannedValues == nil && p.Values != nil {
		visit(p.Values, records)
		source = "State snapshot (not unapplied configuration)"
	}
	changes := map[string]ResourceChange{}
	for _, c := range p.ResourceChanges {
		changes[c.Address] = c
		if c.Type == "hue_scene" {
			records[c.Address] = Resource{Address: c.Address, Type: c.Type, Values: c.Change.After}
		}
	}
	result := View{Version: clean(p.TerraformVersion), Source: source}
	for addr, r := range records {
		if r.Type != "hue_scene" {
			continue
		}
		old := before[addr].Values
		next := r.Values
		kind := "no-op"
		if c, ok := changes[addr]; ok {
			old = masked(c.Change.Before, c.Change.BeforeSensitive)
			next = masked(c.Change.After, c.Change.AfterSensitive)
			next = unknown(next, c.Change.Unknown)
			kind = strings.Join(c.Change.Actions, " / ")
		}
		s := Scene{Address: clean(addr), Name: field(next, "name"), Group: field(next, "group"), Change: kind, Changed: kind != "no-op"}
		if s.Name == "" {
			s.Name = field(old, "name")
		}
		if s.Group == "" {
			s.Group = field(old, "group")
		}
		if n := names[s.Group]; n != "" {
			s.Group = n
		}
		s.Palette = paletteRows(old["palette"], next["palette"], s.Changed)
		a, _ := old["actions"].(map[string]any)
		b, _ := next["actions"].(map[string]any)
		if special(field(next, "actions")) {
			s.Notice = "Actions " + field(next, "actions")
		}
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		for id := range keys {
			oa, ook := a[id]
			na, nok := b[id]
			row := Row{ID: clean(id), Name: names[id], Before: sample(oa, ook), After: sample(na, nok)}
			row.Changed = s.Changed && (!same(oa, na) || ook != nok)
			s.Rows = append(s.Rows, row)
		}
		sort.Slice(s.Rows, func(i, j int) bool { return s.Rows[i].ID < s.Rows[j].ID })
		result.Scenes = append(result.Scenes, s)
	}
	sort.Slice(result.Scenes, func(i, j int) bool { return result.Scenes[i].Address < result.Scenes[j].Address })
	return result, nil
}

const redacted = "(sensitive)"
const pending = "(known after apply)"

func special(s string) bool { return s == redacted || s == pending }
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
func field(m map[string]any, k string) string {
	v := m[k]
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return clean(s)
	}
	return fmt.Sprint(v)
}
func same(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func masked(m map[string]any, flags any) map[string]any {
	if m == nil {
		return nil
	}
	v := mask(m, flags)
	if o, ok := v.(map[string]any); ok {
		return o
	}
	return map[string]any{"name": redacted, "group": redacted, "actions": redacted, "palette": redacted}
}
func mask(v, flags any) any {
	if b, ok := flags.(bool); ok && b {
		return redacted
	}
	switch x := v.(type) {
	case map[string]any:
		o := map[string]any{}
		f, _ := flags.(map[string]any)
		for k, a := range x {
			o[k] = mask(a, f[k])
		}
		return o
	case []any:
		o := make([]any, len(x))
		f, _ := flags.([]any)
		for i, a := range x {
			var flag any
			if i < len(f) {
				flag = f[i]
			}
			o[i] = mask(a, flag)
		}
		return o
	}
	return v
}
func unknown(m map[string]any, flags any) map[string]any {
	if b, ok := flags.(bool); ok && b {
		return map[string]any{"name": pending, "group": pending, "actions": pending, "palette": pending}
	}
	f, _ := flags.(map[string]any)
	if len(f) == 0 {
		return m
	}
	if m == nil {
		m = map[string]any{}
	}
	for k, v := range f {
		if b, ok := v.(bool); ok && b {
			if m[k] != redacted {
				m[k] = pending
			}
		} else if _, ok := v.(map[string]any); ok {
			if s, ok := m[k].(string); ok && s == redacted {
				continue
			}
			child, _ := m[k].(map[string]any)
			m[k] = unknown(child, v)
		}
	}
	return m
}
