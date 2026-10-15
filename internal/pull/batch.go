package pull

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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

type moduleCall struct {
	source   string
	literal  bool
	expanded bool
}

// ConfigModules maps module addresses ("" is the root) to their local source
// directories in one walk of the module tree. import-blocks only reads these
// directories, so shared sources, count, for_each, override files and JSON
// configuration are all fine. The second result lists modules that cannot
// receive import blocks, with the reason; registry and other non-local
// modules have no readable directory and appear only there.
func ConfigModules(root string) (map[string]string, map[string]string, error) {
	dirs, unusable := map[string]string{}, map[string]string{}
	stack := map[string]bool{}
	var walk func(dir, scope, reason string, depth int) error
	walk = func(dir, scope, reason string, depth int) error {
		if depth > 32 || len(dirs) >= 1024 {
			return fmt.Errorf("module tree exceeds supported size")
		}
		if stack[dir] {
			return fmt.Errorf("cyclic local module source: %s", dir)
		}
		stack[dir] = true
		defer delete(stack, dir)
		dirs[scope] = dir
		if reason != "" {
			unusable[scope] = reason
		}
		blocks, _, err := configBlocks(dir, hcl.BlockHeaderSchema{Type: "module", LabelNames: []string{"name"}})
		if err != nil {
			return err
		}
		// Override files come last, so their attributes win as in Terraform.
		calls := map[string]moduleCall{}
		for _, b := range blocks {
			attrs, err := blockAttributes(b, "source", "count", "for_each")
			if err != nil {
				return err
			}
			call := calls[b.Labels[0]]
			if a := attrs["source"]; a != nil {
				call.source, call.literal = literalString(a.Expr)
			}
			if attrs["count"] != nil || attrs["for_each"] != nil {
				call.expanded = true
			}
			calls[b.Labels[0]] = call
		}
		names := make([]string, 0, len(calls))
		for name := range calls {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			call := calls[name]
			child := "module." + name
			if scope != "" {
				child = scope + "." + child
			}
			childReason := reason
			if childReason == "" && call.expanded {
				childReason = "it uses count or for_each, so import addresses need an instance key"
			}
			if !call.literal || !(strings.HasPrefix(call.source, "./") || strings.HasPrefix(call.source, "../")) {
				if childReason == "" {
					childReason = "its source is not a local directory"
				}
				unusable[child] = childReason
				continue
			}
			if err := walk(filepath.Join(dir, call.source), child, childReason, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	root, err := filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, nil, err
	}
	return dirs, unusable, walk(root, "", "", 0)
}

// CheckProviderEnvironment refuses provider "hue" blocks whose host or
// application_key cannot be matched to the environment hue-tf uses.
func CheckProviderEnvironment(modules map[string]string, host, key string) error {
	seen := map[string]bool{}
	for _, dir := range modules {
		// A shared source appears under several module addresses.
		if seen[dir] {
			continue
		}
		seen[dir] = true
		blocks, paths, err := configBlocks(dir, hcl.BlockHeaderSchema{Type: "provider", LabelNames: []string{"name"}})
		if err != nil {
			return err
		}
		for i, b := range blocks {
			if b.Labels[0] != "hue" {
				continue
			}
			attrs, err := blockAttributes(b, "host", "application_key")
			if err != nil {
				return err
			}
			for name, want := range map[string]string{"host": host, "application_key": key} {
				a := attrs[name]
				if a == nil {
					continue
				}
				if v, ok := literalString(a.Expr); !ok || v != want {
					return fmt.Errorf("provider %s in %s cannot be matched to the HUE_BRIDGE environment; use matching literal values or environment defaults", name, paths[i])
				}
			}
		}
	}
	return nil
}
