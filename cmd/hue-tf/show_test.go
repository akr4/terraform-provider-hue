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

// show reports color temperature in kelvin rounded the same way as the provider.
func TestShowKelvinMatchesProvider(t *testing.T) {
	data := []json.RawMessage{
		json.RawMessage(`{"id":"scene","type":"scene","metadata":{"name":"Warm"},"group":{"rid":"zone","rtype":"zone"},"actions":[{"target":{"rid":"light","rtype":"light"},"action":{"color_temperature":{"mirek":370}}}]}`),
		json.RawMessage(`{"id":"zone","type":"zone","metadata":{"name":"Home"}}`),
	}
	var out bytes.Buffer
	if err := printSceneDetails(data, "scene", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2703 K (370 mirek)") {
		t.Fatal(out.String())
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
	t.Setenv("HUE_BRIDGE_HOST", "fake.local")
	t.Setenv("HUE_BRIDGE_ID", "001788fffe000000")
	t.Setenv("HUE_BRIDGE_APPLICATION_KEY", "test-key")
	deps := dependencies{newClient: func(string, string, string) (*hue.Client, error) {
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

// show flags the extras that the hex swatch does not summarize, and an
// effects_v2 that stops an effect is not one.
func TestShowSceneExtras(t *testing.T) {
	for want, action := range map[string]string{
		"gradient":   `"gradient":{"points":[{"color":{"xy":{"x":0.3,"y":0.4}}},{"color":{"xy":{"x":0.4,"y":0.4}}}],"mode":"interpolated_palette"}`,
		"effects":    `"effects":{"effect":"fire"}`,
		"effects_v2": `"effects_v2":{"action":{"effect":"candle","parameters":{"speed":0.4}}}`,
		"":           `"effects_v2":{"action":{"effect":"no_effect"}}`,
	} {
		data := []json.RawMessage{json.RawMessage(`{"id":"scene","type":"scene","metadata":{"name":"Fireplace"},"group":{"rid":"zone","rtype":"zone"},"actions":[{"target":{"rid":"light","rtype":"light"},"action":{"on":{"on":true},` + action + `}}]}`)}
		var out bytes.Buffer
		if err := printSceneDetails(data, "scene", &out); err != nil {
			t.Fatal(err)
		}
		// The action is the last row, and its last column lists the extras.
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		fields := strings.Fields(lines[len(lines)-1])
		got := fields[len(fields)-1]
		if got == "-" {
			got = ""
		}
		if got != want {
			t.Errorf("%s flagged as %q:\n%s", action, got, out.String())
		}
	}
}
