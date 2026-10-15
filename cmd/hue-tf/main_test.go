package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/akr4/terraform-provider-hue/internal/fakebridge"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"raw"}, {"raw", "/clip/v2/resource", "extra"}, {"ls", "invalid"}, {"ls", "room", "--bad"}, {"init", "extra"}, {"discover", "extra"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
func TestCLIHelpAndVersion(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{arg}, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out.String(), "Usage: hue-tf") || !strings.Contains(out.String(), "import-blocks") {
			t.Fatalf("%s: %s", arg, out.String())
		}
	}
	for _, arg := range []string{"version", "--version"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{arg}, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if out.String() != "hue-tf dev\n" {
			t.Fatalf("%s: %q", arg, out.String())
		}
	}
	err := run(context.Background(), []string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--help") {
		t.Fatal(err)
	}
}

// go install builds print the version in the same form as release builds.
func TestCLIVersionForms(t *testing.T) {
	for _, c := range []struct{ version, module, want string }{
		{"0.1.1", "", "0.1.1"},
		{"dev", "v0.1.1", "0.1.1"},
		{"dev", "v0.1.2-0.20261015120000-abcdef123456", "0.1.2-0.20261015120000-abcdef123456"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Version: c.module}}
		if got := buildVersion(c.version, info); got != c.want {
			t.Errorf("buildVersion(%q, %q) = %q, want %q", c.version, c.module, got, c.want)
		}
	}
	if got := buildVersion("dev", nil); got != "dev" {
		t.Errorf("without build info: %q", got)
	}
}

func TestCLIRequiresBridgeEnvironment(t *testing.T) {
	deps := dependencies{newClient: func(string, string, string) (*hue.Client, error) {
		t.Fatal("contacted a bridge without its settings")
		return nil, nil
	}}
	for _, c := range []struct{ host, id, key, want string }{
		{"", "", "", "HUE_BRIDGE_APPLICATION_KEY is not set; run hue-tf init"},
		{"", "", "test-key", "HUE_BRIDGE_HOST is not set; run hue-tf discover"},
		{"192.0.2.1", "", "test-key", "HUE_BRIDGE_ID is not set; run hue-tf discover"},
		{"192.0.2.1", "001788fffe000000", "", "HUE_BRIDGE_APPLICATION_KEY is not set"},
	} {
		t.Setenv("HUE_BRIDGE_HOST", c.host)
		t.Setenv("HUE_BRIDGE_ID", c.id)
		t.Setenv("HUE_BRIDGE_APPLICATION_KEY", c.key)
		for _, args := range [][]string{{"ls", "light"}, {"show", "11111111-1111-4111-8111-111111111111"}, {"recall", "11111111-1111-4111-8111-111111111111"}} {
			err := runWith(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, deps)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v with host %q, ID %q, key %q: %v", args, c.host, c.id, c.key, err)
			}
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
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(host, key, id string) (*hue.Client, error) {
		if host != "fake.local" || key != "test-key" || id != "001788fffe000000" {
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
	t.Setenv("HUE_BRIDGE_ID", "")
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
	var gotID string
	deps := dependencies{
		discover: func(context.Context) ([]bridge, error) {
			return []bridge{{Host: "192.0.2.1", ID: "001788fffe5a5a5a"}}, nil
		},
		observe: func(context.Context, string) (string, error) {
			t.Fatal("observed a certificate although discovery named the bridge")
			return "", nil
		},
		newClient: func(host, key, id string) (*hue.Client, error) {
			if host != "192.0.2.1" || key != "" {
				t.Fatal("invalid selected host/key")
			}
			gotID = id
			return hue.NewClientWithHTTP(server.URL, key, server.Client())
		},
	}
	var out, prompt bytes.Buffer
	if err := runWith(context.Background(), []string{"init"}, &out, &prompt, deps); err != nil {
		t.Fatal(err)
	}
	if gotID != "001788fffe5a5a5a" || out.String() != "export HUE_BRIDGE_HOST='192.0.2.1'\nexport HUE_BRIDGE_ID='001788fffe5a5a5a'\nexport HUE_BRIDGE_APPLICATION_KEY='new-key'\n" {
		t.Fatal(gotID, out.String())
	}
	if !strings.Contains(prompt.String(), "link button") || strings.Contains(prompt.String(), "new-key") {
		t.Fatal("invalid prompt")
	}
	deps.discover = func(context.Context) ([]bridge, error) {
		return []bridge{{Host: "192.0.2.1"}, {Host: "192.0.2.2"}}, nil
	}
	if err := runWith(context.Background(), []string{"init"}, &out, &prompt, deps); err == nil {
		t.Fatal("ambiguous discovery registered a key")
	}
	if calls != 2 {
		t.Fatal("unexpected registration")
	}

	// With HUE_BRIDGE_HOST alone, the first contact reads the ID from the
	// bridge's certificate. With both set, nothing is looked up.
	t.Setenv("HUE_BRIDGE_HOST", "192.0.2.1")
	deps.discover = func(context.Context) ([]bridge, error) {
		t.Fatal("discovered although HUE_BRIDGE_HOST is set")
		return nil, nil
	}
	for _, c := range []struct{ envID, want string }{{"", "001788fffe111111"}, {"001788fffe222222", "001788fffe222222"}} {
		t.Setenv("HUE_BRIDGE_ID", c.envID)
		deps.observe = func(_ context.Context, host string) (string, error) {
			if c.envID != "" || host != "192.0.2.1" {
				t.Fatal("unexpected certificate lookup")
			}
			return "001788fffe111111", nil
		}
		out.Reset()
		if err := runWith(context.Background(), []string{"init"}, &out, &prompt, deps); err != nil {
			t.Fatal(err)
		}
		if gotID != c.want || !strings.Contains(out.String(), "export HUE_BRIDGE_ID='"+c.want+"'") {
			t.Fatal(gotID, out.String())
		}
	}
}

func TestCLIInitTimeout(t *testing.T) {
	t.Setenv("HUE_BRIDGE_HOST", "192.0.2.1")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	pending := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"error":{"type":101}}]`))
	}))
	defer pending.Close()
	// Reading the body lets the server notice when the client gives up.
	silent := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer silent.Close()
	for _, c := range []struct {
		server *httptest.Server
		want   string
	}{
		{pending, "the link button on bridge 192.0.2.1 was not pressed within 1.5s; run hue-tf init again"},
		{silent, "could not reach bridge 192.0.2.1 within 1.5s"},
	} {
		deps := dependencies{linkTimeout: 1500 * time.Millisecond, newClient: func(string, string, string) (*hue.Client, error) {
			return hue.NewClientWithHTTP(c.server.URL, "", c.server.Client())
		}}
		err := runWith(context.Background(), []string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}
}

func TestCLIDiscover(t *testing.T) {
	t.Setenv("HUE_BRIDGE_HOST", "192.0.2.9")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "")
	deps := dependencies{discover: func(context.Context) ([]bridge, error) {
		return []bridge{{Host: "192.0.2.1", ID: "001788fffe5a5a5a"}, {Host: "192.0.2.2"}}, nil
	}, newClient: func(string, string, string) (*hue.Client, error) {
		t.Fatal("discover contacted a bridge")
		return nil, nil
	}}
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"discover"}, &out, &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ADDRESS    BRIDGE ID\n192.0.2.1  001788fffe5a5a5a\n192.0.2.2  -\n" {
		t.Fatalf("%q", out.String())
	}
	deps.discover = func(context.Context) ([]bridge, error) { return nil, nil }
	if err := runWith(context.Background(), []string{"discover"}, &bytes.Buffer{}, &bytes.Buffer{}, deps); err == nil {
		t.Fatal("empty discovery succeeded")
	}
}

func TestCLISceneGroups(t *testing.T) {
	b := fakebridge.New()
	defer b.Close()
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(string, string, string) (*hue.Client, error) { return b.Client(), nil }}
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
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	var out bytes.Buffer
	if err := runWith(context.Background(), []string{"ls", "scene"}, &out, &bytes.Buffer{}, dependencies{newClient: func(string, string, string) (*hue.Client, error) { return b.Client(), nil }}); err != nil {
		t.Fatal(err)
	}
	if len(b.Requests()) != 1 || len(strings.Split(strings.TrimSpace(out.String()), "\n")) != 1 {
		t.Fatal(out.String())
	}
}
