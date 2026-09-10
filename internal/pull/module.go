package pull

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func ModuleAddress(address string) string {
	parts := strings.Split(address, ".")
	if len(parts) <= 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

// ModuleDir resolves configuration sources, never Terraform's downloaded cache.
// Shared sources are refused because editing them changes multiple instances.
func ModuleDir(root, address string) (string, error) {
	if _, _, err := ResourceAddress(address); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	paths := map[string]string{"": root}
	counts := map[string]int{root: 1}
	stack := map[string]bool{}
	var walk func(string, string, int) error
	walk = func(dir, module string, depth int) error {
		if depth > 32 || len(paths) > 1024 {
			return fmt.Errorf("module tree exceeds supported size")
		}
		if stack[dir] {
			return fmt.Errorf("cyclic local module source")
		}
		stack[dir] = true
		defer delete(stack, dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, entry := range entries {
			n := entry.Name()
			if strings.HasSuffix(n, ".tf.json") || n == "override.tf" || strings.HasSuffix(n, "_override.tf") {
				return fmt.Errorf("module resolution does not support JSON or override files")
			}
			if !strings.HasSuffix(n, ".tf") {
				continue
			}
			filename := filepath.Join(dir, n)
			src, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			f, d := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
			if d.HasErrors() {
				return fmt.Errorf("cannot parse %s", filename)
			}
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				if b.Type != "module" {
					continue
				}
				if len(b.Labels) != 1 || names[b.Labels[0]] {
					return fmt.Errorf("invalid or duplicate module declaration")
				}
				names[b.Labels[0]] = true
				source := b.Body.Attributes["source"]
				if source == nil {
					return fmt.Errorf("module source is missing")
				}
				value, err := stringLiteral(source.Expr)
				if err != nil {
					return fmt.Errorf("module source must be a literal local path")
				}
				s := value.AsString()
				if !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
					continue
				}
				for _, key := range []string{"count", "for_each", "providers"} {
					if b.Body.Attributes[key] != nil {
						return fmt.Errorf("local module %s: %s is not supported", b.Labels[0], key)
					}
				}
				target, err := filepath.EvalSymlinks(filepath.Join(dir, s))
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, target)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return fmt.Errorf("local module sources must remain inside the root configuration directory")
				}
				for _, part := range strings.Split(rel, string(filepath.Separator)) {
					if part == ".terraform" || part == ".git" {
						return fmt.Errorf("cannot edit module caches or git metadata")
					}
				}
				child := "module." + b.Labels[0]
				if module != "" {
					child = module + "." + child
				}
				paths[child] = target
				counts[target]++
				if err := walk(target, child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return "", err
	}
	target, ok := paths[ModuleAddress(address)]
	if !ok {
		return "", fmt.Errorf("module not found or not a supported local source")
	}
	if counts[target] != 1 {
		return "", fmt.Errorf("module source is shared by multiple calls; refusing to edit shared configuration")
	}
	return target, nil
}
