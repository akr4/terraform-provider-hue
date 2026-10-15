package pull

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// PendingImports prevents another run before apply from duplicating imports.
// Terraform allows import blocks only in the root module, so only root is read.
// Literal import blocks remain ordinary Terraform configuration, not private state.
func PendingImports(root string, managed []ManagedResource) ([]ManagedResource, error) {
	byID, byAddress := map[string]string{}, map[string]string{}
	for _, r := range managed {
		byID[r.ID] = r.Address
		byAddress[r.Address] = r.ID
	}
	blocks, paths, err := configBlocks(root, hcl.BlockHeaderSchema{Type: "import"})
	if err != nil {
		return nil, err
	}
	result := []ManagedResource{}
	for i, b := range blocks {
		path := paths[i]
		attrs, err := blockAttributes(b, "to", "id", "for_each", "provider")
		if err != nil {
			return nil, err
		}
		to := attrs["to"]
		if to == nil {
			return nil, fmt.Errorf("import target missing in %s", path)
		}
		traversal, d := hcl.AbsTraversalForExpr(to.Expr)
		if d.HasErrors() {
			return nil, fmt.Errorf("import target must be a literal address in %s", path)
		}
		// The type comes from the address without instance keys; the keys stay
		// in the recorded address, so for_each and count targets are pending too.
		parts := []string{traversal.RootName()}
		var address strings.Builder
		address.WriteString(traversal.RootName())
		for _, step := range traversal[1:] {
			switch s := step.(type) {
			case hcl.TraverseAttr:
				parts = append(parts, s.Name)
				address.WriteString("." + s.Name)
			case hcl.TraverseIndex:
				address.WriteString(indexKey(s.Key))
			}
		}
		kind, _, err := ResourceAddress(strings.Join(parts, "."))
		if err != nil {
			continue
		} // another provider's import, or a Hue type that import-blocks does not discover
		if attrs["for_each"] != nil {
			return nil, fmt.Errorf("cannot read the UUIDs of a Hue import block with for_each in %s", path)
		}
		var id string
		if a := attrs["id"]; a != nil {
			id, _ = literalString(a.Expr)
		}
		if id == "" {
			return nil, fmt.Errorf("import ID must be a literal string in %s", path)
		}
		target := address.String()
		if old, ok := byAddress[target]; ok {
			if old != id {
				return nil, fmt.Errorf("import address %s already maps to another ID", target)
			}
			continue
		}
		if old, ok := byID[id]; ok {
			return nil, fmt.Errorf("import UUID %s already maps to %s", id, old)
		}
		byAddress[target], byID[id] = id, target
		result = append(result, ManagedResource{Address: target, ID: id, Kind: kind})
	}
	return result, nil
}

// indexKey formats an instance key the way Terraform prints addresses.
func indexKey(key cty.Value) string {
	switch {
	case key.Type() == cty.Number && key.IsKnown() && !key.IsNull():
		return "[" + key.AsBigFloat().Text('f', -1) + "]"
	case key.Type() == cty.String && key.IsKnown() && !key.IsNull():
		return fmt.Sprintf("[%q]", key.AsString())
	}
	return "[?]"
}
