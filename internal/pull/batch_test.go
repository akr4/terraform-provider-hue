package pull

import (
	"os"
	"strings"
	"testing"
)

func TestSyncProviderEnvironment(t *testing.T) {
	dir := t.TempDir()
	source := `provider "hue" { application_key = "different-secret" }`
	if e := os.WriteFile(dir+"/main.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	e := CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key")
	if e == nil || strings.Contains(e.Error(), "different-secret") {
		t.Fatalf("%v", e)
	}
	if e = os.WriteFile(dir+"/main.tf", []byte(`provider "hue" {}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key"); e != nil {
		t.Fatal(e)
	}
}
