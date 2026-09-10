package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"raw"}, {"raw", "/clip/v2/resource", "extra"}, {"ls", "invalid"}, {"ls", "room", "--bad"}, {"init", "extra"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
func TestShellQuote(t *testing.T) {
	if got := shellQuote("abc'$(id)"); got != "'abc'\"'\"'$(id)'" {
		t.Fatal(got)
	}
	if strings.Contains(shellQuote("normal"), "\n") {
		t.Fatal("unexpected newline")
	}
}

func TestCLIReadCommands(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(host, key string) (*hue.Client, error) {
		if host != "fake.local" || key != "test-key" {
			t.Fatal("environment not passed")
		}
		return b.Client(), nil
	}}
	for _, args := range [][]string{{"ls", "light"}, {"ls", "light", "--json"}, {"raw", "/clip/v2/resource/light"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := runWith(context.Background(), args, &out, &bytes.Buffer{}, deps); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), fakebridge.LightID) {
				t.Fatal(out.String())
			}
			if args[0] == "raw" || len(args) == 3 {
				if !json.Valid(out.Bytes()) {
					t.Fatal("invalid JSON")
				}
			}
		})
	}
}
func TestCLIInit(t *testing.T) {
	t.Setenv("HUE_BRIDGE_HOST", "")
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api" || r.Header.Get("hue-application-key") != "" {
			t.Error("invalid registration request")
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`[{"error":{"type":101}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"success":{"username":"new-key"}}]`))
	}))
	defer server.Close()
	deps := dependencies{discover: func(context.Context) ([]string, error) { return []string{"192.0.2.1"}, nil }, newClient: func(host, key string) (*hue.Client, error) {
		if host != "192.0.2.1" || key != "" {
			t.Fatal("invalid selected host/key")
		}
		return hue.NewClientWithHTTP(server.URL, key, server.Client())
	}}
	var out, prompt bytes.Buffer
	if err := runWith(context.Background(), []string{"init"}, &out, &prompt, deps); err != nil {
		t.Fatal(err)
	}
	if out.String() != "export HUE_BRIDGE_HOST='192.0.2.1'\nexport HUE_BRIDGE_APPLICATION_KEY='new-key'\n" {
		t.Fatal(out.String())
	}
	if !strings.Contains(prompt.String(), "link button") || strings.Contains(prompt.String(), "new-key") {
		t.Fatal("invalid prompt")
	}
	deps.discover = func(context.Context) ([]string, error) { return []string{"192.0.2.1", "192.0.2.2"}, nil }
	if err := runWith(context.Background(), []string{"init"}, &out, &prompt, deps); err == nil {
		t.Fatal("ambiguous discovery registered a key")
	}
	if calls != 2 {
		t.Fatal("unexpected registration")
	}
}

func TestCLISceneGroups(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }}
	room1 := hue.Reference{RID: "55555555-5555-4555-8555-555555555555", RType: "room"}
	room2 := hue.Reference{RID: "66666666-6666-4666-8666-666666666666", RType: "room"}
	zone := hue.Reference{RID: "77777777-7777-4777-8777-777777777777", RType: "zone"}
	for _, ref := range []hue.Reference{room1, room2, zone} {
		b.Put(ref.RType, ref.RID, hue.Group{ID: ref.RID, Metadata: hue.Metadata{Name: "寝室"}})
	}
	refs := []hue.Reference{room1, room2, zone, {RID: "88888888-8888-4888-8888-888888888888", RType: "room"}}
	for i, ref := range refs {
		id := fmt.Sprintf("99999999-9999-4999-8999-%012d", i)
		b.Put("scene", id, hue.Scene{ID: id, Metadata: hue.Metadata{Name: "リラックス"}, Group: ref})
	}
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"ls", "scene"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatal(out.String())
	}
	for i, ref := range refs {
		id := fmt.Sprintf("99999999-9999-4999-8999-%012d", i)
		matched := false
		for _, line := range lines[1:] {
			if strings.HasPrefix(line, id) {
				name := "寝室"
				if i == 3 {
					name = "(unknown)"
				}
				if !strings.Contains(line, ref.RID) || !strings.Contains(line, ref.RType) || !strings.Contains(line, name) || !strings.Contains(line, "リラックス") {
					t.Fatal(line)
				}
				matched = true
			}
		}
		if !matched {
			t.Fatalf("scene missing: %s", id)
		}
	}
	counts := map[string]int{}
	for _, req := range b.Requests() {
		counts[req.Path]++
		if req.Method != "GET" {
			t.Fatal("listing wrote to bridge")
		}
	}
	for _, kind := range []string{"scene", "room", "zone"} {
		if counts["/clip/v2/resource/"+kind] != 1 {
			t.Fatal(counts)
		}
	}
	before := len(b.Requests())
	out.Reset()
	if err := runWith(context.Background(), []string{"ls", "scene", "--json"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	var scenes []hue.Scene
	if err := json.Unmarshal(out.Bytes(), &scenes); err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 4 || len(b.Requests()) != before+1 {
		t.Fatal("JSON listing changed or requested group names")
	}
	for _, scene := range scenes {
		if scene.Metadata.Name != "リラックス" || scene.Group.RID == "" {
			t.Fatal(scene)
		}
	}
}
func TestCLISceneEmpty(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"ls", "scene"}, &out, &bytes.Buffer{}, dependencies{newClient: func(string, string) (*hue.Client, error) { return b.Client(), nil }}); err != nil {
		t.Fatal(err)
	}
	if len(b.Requests()) != 1 || len(strings.Split(strings.TrimSpace(out.String()), "\n")) != 1 {
		t.Fatal(out.String())
	}
}
