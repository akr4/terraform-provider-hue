package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

const opScene = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const opSmart = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const opDevice = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
const opLight = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

func TestOperateValidation(t *testing.T) {
	for _, args := range [][]string{
		{"recall"}, {"identify"}, {"identify", opDevice, "--count", "0"}, {"identify", opDevice, "--count", "11"}, {"identify", opDevice, "--count"}, {"identify", opDevice, "--count", "2", "--count", "3"}, {"recall", opScene, "--count", "3"}, {"recall", "../scene"}, {"identify", "bad"},
		{"recall", opScene, opSmart}, {"identify", opDevice, "--action", "identify"},
		{"recall", opScene, "--action"}, {"recall", opScene, "--action", "bad"},
		{"recall", opScene, "--action", "active", "--action", "active"},
	} {
		if err := runWith(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, dependencies{newClient: func(string, string) (*hue.Client, error) { t.Fatal("invalid args accessed bridge"); return nil, nil }}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestOperateRequests(t *testing.T) {
	for _, tc := range []struct {
		name                string
		args                []string
		path, field, action string
		status              int
		wantErr             bool
	}{
		{"scene", []string{"recall", opScene}, "scene/" + opScene, "recall", "active", 200, false},
		{"uppercase", []string{"recall", strings.ToUpper(opScene)}, "scene/" + opScene, "recall", "active", 200, false},
		{"dynamic", []string{"recall", opScene, "--action", "dynamic_palette"}, "scene/" + opScene, "recall", "dynamic_palette", 200, false},
		{"static", []string{"recall", opScene, "--action", "static"}, "scene/" + opScene, "recall", "static", 200, false},
		{"smart", []string{"recall", opSmart}, "smart_scene/" + opSmart, "recall", "activate", 200, false},
		{"stop smart", []string{"recall", opSmart, "--action", "deactivate"}, "smart_scene/" + opSmart, "recall", "deactivate", 200, false},
		{"identify once", []string{"identify", opDevice, "--count", "1"}, "device/" + opDevice, "identify", "identify", 200, false},
		{"identify twice", []string{"identify", opDevice, "--count", "2"}, "device/" + opDevice, "identify", "identify", 200, false},
		{"identify device", []string{"identify", opDevice}, "device/" + opDevice, "identify", "identify", 200, false},
		{"identify light owner", []string{"identify", opLight}, "device/" + opDevice, "identify", "identify", 200, false},
		{"wrong scene action", []string{"recall", opScene, "--action", "activate"}, "", "", "", 200, true},
		{"wrong smart action", []string{"recall", opSmart, "--action", "dynamic_palette"}, "", "", "", 200, true},
		{"wrong recall type", []string{"recall", opDevice}, "", "", "", 200, true},
		{"wrong identify type", []string{"identify", opScene}, "", "", "", 200, true},
		{"missing", []string{"recall", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}, "", "", "", 200, true},
		{"write failure", []string{"recall", opScene}, "scene/" + opScene, "recall", "active", 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
			gets, puts := 0, 0
			waits := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("hue-application-key") != "test-key" {
					t.Error("missing authentication")
				}
				if r.Method == "GET" {
					gets++
					if r.URL.Path != "/clip/v2/resource" {
						t.Error(r.URL.Path)
					}
					fmt.Fprintf(w, `{"errors":[],"data":[{"id":%q,"type":"scene","metadata":{"name":"Dinner"}},{"id":%q,"type":"smart_scene"},{"id":%q,"type":"device","identify":{}},{"id":%q,"type":"light","owner":{"rid":%q,"rtype":"device"}}]}`, opScene, opSmart, opDevice, opLight, opDevice)
					return
				}
				puts++
				if r.Method != "PUT" || r.URL.Path != "/clip/v2/resource/"+tc.path {
					t.Errorf("unexpected write %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				want := map[string]any{tc.field: map[string]any{"action": tc.action}}
				if !reflect.DeepEqual(body, want) {
					t.Errorf("body=%v want %v", body, want)
				}
				if tc.status != 200 {
					w.WriteHeader(tc.status)
					w.Write([]byte(`{"errors":[{"description":"rejected"}],"data":[]}`))
					return
				}
				w.Write([]byte(`{"errors":[],"data":[]}`))
			}))
			defer server.Close()
			deps := dependencies{wait: func(_ context.Context, d time.Duration) error {
				if d != 3*time.Second {
					t.Fatal(d)
				}
				waits++
				return nil
			}, newClient: func(string, string) (*hue.Client, error) {
				return hue.NewClientWithHTTP(server.URL, "test-key", server.Client())
			}, readState: func(context.Context) ([]byte, error) { t.Fatal("read state"); return nil, nil }, terraform: func(context.Context, ...string) ([]byte, error) { t.Fatal("ran Terraform"); return nil, nil }}
			var out bytes.Buffer
			err := runWith(context.Background(), tc.args, &out, &bytes.Buffer{}, deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			wantPuts := 1
			if tc.args[0] == "identify" {
				wantPuts = 3
				for i, arg := range tc.args {
					if arg == "--count" {
						wantPuts, _ = strconv.Atoi(tc.args[i+1])
					}
				}
			}
			if tc.path == "" {
				wantPuts = 0
			}
			if wantPuts > 1 && waits != wantPuts-1 {
				t.Fatalf("waits=%d", waits)
			}
			if gets != 1 || puts != wantPuts {
				t.Fatalf("GET=%d PUT=%d", gets, puts)
			}
			if tc.wantErr && out.Len() != 0 {
				t.Fatal("reported success on failure")
			}
			if !tc.wantErr && !strings.Contains(out.String(), "Requested "+tc.action) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestOperateRejectsUnresolvableIdentify(t *testing.T) {
	for _, raw := range []string{
		fmt.Sprintf(`{"id":%q,"type":"device"}`, opLight),
		fmt.Sprintf(`{"id":%q,"type":"device","identify":null}`, opLight),
		fmt.Sprintf(`{"id":%q,"type":"light","owner":{"rid":%q,"rtype":"device"}}`, opLight, opDevice),
	} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected write")
					w.WriteHeader(400)
					return
				}
				fmt.Fprintf(w, `{"errors":[],"data":[%s]}`, raw)
			}))
			defer server.Close()
			deps := dependencies{newClient: func(string, string) (*hue.Client, error) {
				return hue.NewClientWithHTTP(server.URL, "test-key", server.Client())
			}}
			if err := runWith(context.Background(), []string{"identify", opLight}, &bytes.Buffer{}, &bytes.Buffer{}, deps); err == nil {
				t.Fatal("accepted unsupported identify target")
			}
		})
	}
}

func TestIdentifyCancelBetweenSignals(t *testing.T) {
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	puts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprintf(w, `{"errors":[],"data":[{"id":%q,"type":"device","identify":{}}]}`, opDevice)
			return
		}
		puts++
		w.Write([]byte(`{"errors":[],"data":[]}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) {
		return hue.NewClientWithHTTP(server.URL, "test-key", server.Client())
	}, wait: func(ctx context.Context, d time.Duration) error { cancel(); return waitForOperation(ctx, d) }}
	err := runWith(ctx, []string{"identify", opDevice}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if err != context.Canceled || puts != 1 {
		t.Fatalf("err=%v PUTs=%d", err, puts)
	}
}
