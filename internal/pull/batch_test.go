package pull

import (
	"os"
	"strings"
	"testing"
)

// A state as Terraform writes it for count, for_each, deposed, module and
// provider-alias instances, plus a hue_device that import-blocks never imports.
const complexState = `{"resources":[
 {"mode":"managed","type":"hue_device","name":"lamp","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"11111111-1111-4111-8111-111111111111"}}]},
 {"mode":"managed","type":"hue_zone","name":"counted","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"index_key":0,"attributes":{"id":"20000000-0000-4000-8000-000000000000"}},{"index_key":1,"attributes":{"id":"21000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_scene","name":"each","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"index_key":"day","attributes":{"id":"30000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_room","name":"replaced","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"40000000-0000-4000-8000-000000000000"}},{"deposed":"1a2b3c4d","attributes":{"id":"41000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_zone","name":"other_bridge","provider":"provider[\"registry.terraform.io/akr4/hue\"].second","instances":[{"attributes":{"id":"50000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","module":"module.rooms[\"office\"]","type":"hue_smart_scene","name":"natural","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"60000000-0000-4000-8000-000000000000"}}]},
 {"mode":"data","type":"hue_light","name":"desk","provider":"provider[\"registry.terraform.io/akr4/hue\"]","instances":[{"attributes":{"id":"70000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"terraform_data","name":"other","provider":"provider[\"terraform.io/builtin/terraform\"]","instances":[{"attributes":{"id":"other"}}]}
]}`

func TestManagedResourcesAcceptsAnyState(t *testing.T) {
	got, err := ManagedResources([]byte(complexState))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"20000000-0000-4000-8000-000000000000": "hue_zone.counted[0]",
		"21000000-0000-4000-8000-000000000000": "hue_zone.counted[1]",
		"30000000-0000-4000-8000-000000000000": `hue_scene.each["day"]`,
		"40000000-0000-4000-8000-000000000000": "hue_room.replaced",
		"41000000-0000-4000-8000-000000000000": "hue_room.replaced",
		"50000000-0000-4000-8000-000000000000": "hue_zone.other_bridge",
		"60000000-0000-4000-8000-000000000000": `module.rooms["office"].hue_smart_scene.natural`,
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for _, r := range got {
		if want[r.ID] != r.Address {
			t.Fatalf("%s: got %s, want %s", r.ID, r.Address, want[r.ID])
		}
	}
	addresses, err := StateAddresses([]byte(complexState))
	if err != nil || !addresses["hue_zone.counted"] || !addresses[`module.rooms["office"].hue_smart_scene.natural`] || addresses["hue_light.desk"] {
		t.Fatalf("%v %v", addresses, err)
	}
}

func TestManagedResourcesRejectsSharedUUID(t *testing.T) {
	state := `{"resources":[
 {"mode":"managed","type":"hue_room","name":"a","instances":[{"attributes":{"id":"40000000-0000-4000-8000-000000000000"}}]},
 {"mode":"managed","type":"hue_room","name":"b","instances":[{"attributes":{"id":"40000000-0000-4000-8000-000000000000"}}]}
]}`
	if _, err := ManagedResources([]byte(state)); err == nil || !strings.Contains(err.Error(), "hue_room.a and hue_room.b") {
		t.Fatal(err)
	}
}

func TestSyncProviderEnvironment(t *testing.T) {
	dir := t.TempDir()
	source := `provider "hue" { application_key = "different-secret" }`
	if e := os.WriteFile(dir+"/main.tf", []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	e := CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key", "001788fffe000000")
	if e == nil || strings.Contains(e.Error(), "different-secret") {
		t.Fatalf("%v", e)
	}
	if e = os.WriteFile(dir+"/main.tf", []byte(`provider "hue" {}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key", "001788fffe000000"); e != nil {
		t.Fatal(e)
	}
	// JSON configuration is checked as well, and a matching literal passes.
	if e = os.WriteFile(dir+"/provider.tf.json", []byte(`{"provider":{"hue":{"host":"other-bridge"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "bridge", "test-key", "001788fffe000000"); e == nil || !strings.Contains(e.Error(), "provider.tf.json") {
		t.Fatalf("%v", e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "other-bridge", "test-key", "001788fffe000000"); e != nil {
		t.Fatal(e)
	}
	// The bridge ID must match too.
	if e = os.WriteFile(dir+"/main.tf", []byte(`provider "hue" { bridge_id = "001788fffe111111" }`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = CheckProviderEnvironment(map[string]string{"": dir}, "other-bridge", "test-key", "001788fffe000000"); e == nil || !strings.Contains(e.Error(), "bridge_id") {
		t.Fatalf("%v", e)
	}
}
