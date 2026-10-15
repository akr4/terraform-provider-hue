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

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type ManagedResource struct {
	Address, ID, Kind string
}

// importable lists the resource types that import-blocks discovers on the bridge.
var importable = map[string]bool{"hue_room": true, "hue_zone": true, "hue_scene": true, "hue_smart_scene": true, "hue_behavior_instance": true}

type stateResource struct {
	Mode, Type, Name, Module string
	Instances                []struct {
		IndexKey   json.RawMessage `json:"index_key"`
		Deposed    string
		Attributes struct{ ID string }
	}
}

func decodeState(data []byte) ([]stateResource, error) {
	var s struct{ Resources []stateResource }
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid Terraform state")
	}
	return s.Resources, nil
}

// ManagedResources lists every managed instance of an importable type, so its
// UUID is never proposed for import again. Count and for_each instances,
// deposed objects, module instances and provider aliases are all included.
// Other Hue types, such as hue_device, are not import candidates and are skipped.
func ManagedResources(data []byte) ([]ManagedResource, error) {
	resources, err := decodeState(data)
	if err != nil {
		return nil, err
	}
	var result []ManagedResource
	seen := map[string]string{}
	for _, r := range resources {
		if r.Mode != "managed" || !importable[r.Type] {
			continue
		}
		base := r.Type + "." + r.Name
		if r.Module != "" {
			base = r.Module + "." + base
		}
		for _, i := range r.Instances {
			address := base
			if len(i.IndexKey) > 0 {
				address += "[" + string(i.IndexKey) + "]"
			}
			id := i.Attributes.ID
			if id == "" {
				continue
			}
			if other, ok := seen[id]; ok {
				if other != address {
					return nil, fmt.Errorf("UUID %s is managed at multiple addresses: %s and %s", id, other, address)
				}
				continue
			}
			seen[id] = address
			result = append(result, ManagedResource{Address: address, ID: id, Kind: strings.TrimPrefix(r.Type, "hue_")})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, nil
}

// StateAddresses returns the address of every managed resource in the state,
// without instance keys, so a new import never targets an address in use.
func StateAddresses(data []byte) (map[string]bool, error) {
	resources, err := decodeState(data)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, r := range resources {
		if r.Mode != "managed" {
			continue
		}
		address := r.Type + "." + r.Name
		if r.Module != "" {
			address = r.Module + "." + address
		}
		result[address] = true
	}
	return result, nil
}

// RemoteDefinition projects supported writable attributes, excluding runtime fields.

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
