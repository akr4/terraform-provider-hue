package pull

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func ResourceAddress(address string) (kind, name string, err error) {
	parts := strings.Split(address, ".")
	if len(parts) < 2 || len(parts)%2 != 0 {
		return "", "", fmt.Errorf("expected [module.NAME.]hue_TYPE.NAME")
	}
	for i := 0; i < len(parts)-2; i += 2 {
		if parts[i] != "module" || !hclsyntax.ValidIdentifier(parts[i+1]) {
			return "", "", fmt.Errorf("only unindexed module addresses are supported")
		}
	}
	parts = parts[len(parts)-2:]
	if !hclsyntax.ValidIdentifier(parts[1]) {
		return "", "", fmt.Errorf("invalid resource name")
	}
	kind = strings.TrimPrefix(parts[0], "hue_")
	if parts[0] != "hue_"+kind || (kind != "room" && kind != "zone" && kind != "scene" && kind != "smart_scene" && kind != "behavior_instance") {
		return "", "", fmt.Errorf("unsupported resource type")
	}
	return kind, parts[1], nil
}

func ModuleAddress(address string) string {
	parts := strings.Split(address, ".")
	if len(parts) <= 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}
