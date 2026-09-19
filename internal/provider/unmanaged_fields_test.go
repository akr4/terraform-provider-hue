package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
)

// Assert the actual update wire contract, including omission rather than null.
// This does not establish the physical Bridge's partial-update semantics.
func assertUnmanagedFieldsOmitted(t *testing.T, b *fakebridge.Bridge, kind string, fields, metadataFields []string) {
	t.Helper()
	updates := 0
	for _, req := range b.Requests() {
		if req.Method != "PUT" || !strings.HasPrefix(req.Path, "/clip/v2/resource/"+kind+"/") {
			continue
		}
		updates++
		var body map[string]json.RawMessage
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		for _, key := range fields {
			if _, exists := body[key]; exists {
				t.Fatalf("%s update sent unmanaged %s: %s", kind, key, req.Body)
			}
		}
		var metadata map[string]json.RawMessage
		if raw, exists := body["metadata"]; exists {
			if err := json.Unmarshal(raw, &metadata); err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range metadataFields {
			if _, exists := metadata[key]; exists {
				t.Fatalf("%s update sent unmanaged metadata.%s: %s", kind, key, req.Body)
			}
		}
	}
	if updates == 0 {
		t.Fatalf("no %s updates were exercised", kind)
	}
}
