package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"net/http"
	"net/http/httptest"
)

func TestShowSceneDetails(t *testing.T) {
	raw := []string{
		`{"id":"scene","type":"scene","metadata":{"name":"Dinner"},"group":{"rid":"zone","rtype":"zone"},"actions":[{"target":{"rid":"light","rtype":"light"},"action":{"on":{"on":true},"dimming":{"brightness":12.5},"color":{"xy":{"x":0.1554,"y":0.0996}},"color_temperature":{"mirek":null}}}]}`,
		`{"id":"zone","type":"zone","metadata":{"name":"Home"}}`,
		`{"id":"room","type":"room","metadata":{"name":"Kitchen"},"children":[{"rid":"device","rtype":"device"}]}`,
		`{"id":"light","type":"light","metadata":{"name":"Sink"},"owner":{"rid":"device","rtype":"device"}}`,
		`{"id":"behavior","type":"behavior_instance","metadata":{"name":"Dining button"},"enabled":false,"configuration":{"buttons":{"button2":{"recall":{"rid":"scene","rtype":"scene"}}}}}`,
		`{"id":"smart","type":"smart_scene","metadata":{"name":"Natural"},"week_timeslots":[{"recurrence":["monday"],"timeslots":[{"start_time":{"kind":"sunset"},"target":{"rid":"scene","rtype":"scene"}}]}]}`,
		`{"id":"unrelated","type":"behavior_instance","metadata":{"name":"No reference"},"configuration":{"text":"scene","target":{"rid":"scene","rtype":"light"}}}`,
	}
	data := []json.RawMessage{}
	for _, r := range raw {
		data = append(data, json.RawMessage(r))
	}
	var out bytes.Buffer
	if err := printSceneDetails(data, "scene", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Dinner", "Home", "Kitchen", "Sink", "12.5%", "#", "Dining button", `["buttons"]["button2"]`, "Natural", "v2 only", "disabled automations", "Manual use and v1 rules are not inspected"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "No reference") || strings.Contains(out.String(), "mirek") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := printSceneDetails(data, "smart", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sunset", "monday", "Dinner", "None found"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
	for _, id := range []string{"missing", "light"} {
		if err := printSceneDetails(data, id, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
}

func TestShowCLI(t *testing.T) {
	for _, args := range [][]string{{"show"}, {"show", "bad"}, {"show", "00000000-0000-0000-0000-000000000000", "--write"}} {
		if err := runWith(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, dependencies{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/clip/v2/resource" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":[{"id":"00000000-0000-0000-0000-000000000000","type":"scene","metadata":{"name":"Test scene"},"actions":[]}],"errors":[]}`))
	}))
	defer server.Close()
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(string, string) (*hue.Client, error) {
		return hue.NewClientWithHTTP(server.URL, "test-key", server.Client())
	}}
	var out bytes.Buffer
	err := runWith(context.Background(), []string{"show", "00000000-0000-0000-0000-000000000000"}, &out, &bytes.Buffer{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(out.String(), "Test scene") {
		t.Fatalf("requests=%d output=%s", calls, out.String())
	}
}
