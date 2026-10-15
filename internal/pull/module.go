package pull

import "strings"

func ModuleAddress(address string) string {
	parts := strings.Split(address, ".")
	if len(parts) <= 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}
