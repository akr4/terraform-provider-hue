package pull

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// PendingImports prevents another pull before apply from duplicating definitions.
// Literal import blocks remain ordinary Terraform configuration, not private state.
func PendingImports(modules map[string]string, managed []ManagedResource) ([]ManagedResource, error) {
	byID, byAddress := map[string]string{}, map[string]string{}
	for _, r := range managed {
		byID[r.ID] = r.Address
		byAddress[r.Address] = r.ID
	}
	result := []ManagedResource{}
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
			f, d := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
			if d.HasErrors() {
				return nil, fmt.Errorf("cannot parse %s", path)
			}
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				if b.Type != "import" {
					continue
				}
				to := b.Body.Attributes["to"]
				if to == nil {
					return nil, fmt.Errorf("import target missing in %s", path)
				}
				traversal, d := hcl.AbsTraversalForExpr(to.Expr)
				if d.HasErrors() {
					return nil, fmt.Errorf("import target must be an unindexed literal address in %s", path)
				}
				parts := []string{traversal.RootName()}
				indexed := false
				for _, step := range traversal[1:] {
					a, ok := step.(hcl.TraverseAttr)
					if !ok {
						indexed = true
						continue
					}
					parts = append(parts, a.Name)
				}
				address := strings.Join(parts, ".")
				if scope != "" {
					address = scope + "." + address
				}
				kind, _, err := ResourceAddress(address)
				if err != nil {
					continue
				} // another provider's import
				if indexed {
					return nil, fmt.Errorf("indexed imports are not supported by pull: %s", path)
				}
				idAttr := b.Body.Attributes["id"]
				if idAttr == nil || b.Body.Attributes["for_each"] != nil || b.Body.Attributes["provider"] != nil {
					return nil, fmt.Errorf("pull requires literal Hue import blocks without for_each/provider: %s", path)
				}
				idValue, err := stringLiteral(idAttr.Expr)
				if err != nil {
					return nil, fmt.Errorf("import ID must be literal for pull: %s", path)
				}
				id := idValue.AsString()
				if old, ok := byAddress[address]; ok {
					if old != id {
						return nil, fmt.Errorf("import address %s already maps to another ID", address)
					}
					continue
				}
				if old, ok := byID[id]; ok {
					return nil, fmt.Errorf("import UUID %s already maps to %s", id, old)
				}
				byAddress[address], byID[id] = id, address
				result = append(result, ManagedResource{Address: address, ID: id, Kind: kind, Attributes: map[string]json.RawMessage{}})
			}
		}
	}
	return result, nil
}

func HasResourceDefinition(dir, kind, name string) (bool, error) {
	_, _, err := findResource(dir, kind, name)
	if errors.Is(err, ErrResourceDefinitionNotFound) {
		return false, nil
	}
	return err == nil, err
}
