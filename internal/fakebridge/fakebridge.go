// Package fakebridge implements a synthetic Hue API v2 server for tests.
// Fixtures are hand-authored, not captured from a physical Hue bridge.
package fakebridge

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

//go:embed testdata/resources.json
var fixture []byte

const LightID = "11111111-1111-4111-8111-111111111111"
const DeviceID = "22222222-2222-4222-8222-222222222222"
const WhiteLightID = "33333333-3333-4333-8333-333333333333"

type Request struct {
	Method, Path string
	Body         json.RawMessage
}
type Bridge struct {
	Server    *httptest.Server
	mu        sync.Mutex
	resources map[string]map[string]json.RawMessage
	requests  []Request
}

func New() *Bridge {
	b := &Bridge{resources: map[string]map[string]json.RawMessage{}}
	var data map[string][]json.RawMessage
	if err := json.Unmarshal(fixture, &data); err != nil {
		panic(err)
	}
	for kind, items := range data {
		b.resources[kind] = map[string]json.RawMessage{}
		for _, item := range items {
			var id struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(item, &id); err != nil {
				panic(err)
			}
			b.resources[kind][id.ID] = item
		}
	}
	b.resources["behavior_instance"] = map[string]json.RawMessage{}
	b.resources["behavior_script"] = map[string]json.RawMessage{}
	b.resources["button"] = map[string]json.RawMessage{}
	b.Server = httptest.NewTLSServer(http.HandlerFunc(b.serve))
	return b
}
func (b *Bridge) Close() { b.Server.Close() }
func (b *Bridge) Client() *hue.Client {
	client, err := hue.NewClientWithHTTP(b.Server.URL, "test-key", b.Server.Client())
	if err != nil {
		panic(err)
	}
	return client
}
func (b *Bridge) Requests() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Request(nil), b.requests...)
}
func (b *Bridge) Put(kind, id string, value any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	b.resources[kind][id] = raw
}
func (b *Bridge) Remove(kind, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.resources[kind], id)
}
func (b *Bridge) Count(kind string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.resources[kind])
}
func (b *Bridge) ReadScene(ctx context.Context, id string) (hue.Scene, error) {
	return hue.GetOne[hue.Scene](ctx, b.Client(), "scene", id)
}
func failure(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"description": message}}, "data": []any{}})
}
func success(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{}, "data": data})
}
func (b *Bridge) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("hue-application-key") != "test-key" {
		failure(w, 403, "unauthorized")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/clip/v2/resource/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/clip/v2/resource/") || len(parts) > 2 {
		failure(w, 404, "not found")
		return
	}
	kind := parts[0]
	items, ok := b.resources[kind]
	if !ok {
		failure(w, 404, "unknown type")
		return
	}
	id := ""
	if len(parts) == 2 {
		id = parts[1]
	}
	var body json.RawMessage
	if r.Method == "POST" || r.Method == "PUT" {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			failure(w, 400, "invalid JSON")
			return
		}
	}
	b.requests = append(b.requests, Request{Method: r.Method, Path: r.URL.Path, Body: body})
	switch r.Method {
	case "GET":
		if id != "" {
			value, ok := items[id]
			if !ok {
				failure(w, 404, "not found")
				return
			}
			success(w, []json.RawMessage{value})
			return
		}
		ids := []string{}
		for id := range items {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		data := []json.RawMessage{}
		for _, id := range ids {
			data = append(data, items[id])
		}
		success(w, data)
	case "POST", "PUT":
		if kind != "room" && kind != "zone" && kind != "scene" && kind != "behavior_instance" {
			failure(w, 405, "read only")
			return
		}
		if r.Method == "PUT" {
			if _, ok := items[id]; !ok {
				failure(w, 404, "not found")
				return
			}
		} else {
			if id != "" {
				failure(w, 405, "invalid collection")
				return
			}
			bytes := make([]byte, 16)
			if _, err := rand.Read(bytes); err != nil {
				failure(w, 500, "random ID failed")
				return
			}
			id = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
		}
		value := map[string]json.RawMessage{}
		if r.Method == "PUT" {
			_ = json.Unmarshal(items[id], &value)
		}
		var patch map[string]json.RawMessage
		_ = json.Unmarshal(body, &patch)
		if kind == "behavior_instance" {
			if r.Method != "PUT" {
				failure(w, 405, "behavior creation not supported")
				return
			}
			for key := range patch {
				if key != "metadata" && key != "configuration" && key != "enabled" {
					failure(w, 400, "read-only behavior field")
					return
				}
			}
			if raw, ok := patch["configuration"]; ok {
				if hue.ValidateConfiguration(raw) != nil {
					failure(w, 400, "invalid configuration")
					return
				}
			}
			status := "running"
			if string(patch["enabled"]) == "false" {
				status = "disabled"
			}
			value["status"], _ = json.Marshal(status)
			value["last_error"] = json.RawMessage(`""`)
		}
		if kind == "scene" {
			if _, ok := patch["palette"]; ok {
				failure(w, 400, "palette is read only in fake v0")
				return
			}
			if r.Method == "PUT" {
				var metadata map[string]json.RawMessage
				_ = json.Unmarshal(patch["metadata"], &metadata)
				if _, ok := metadata["image"]; ok {
					failure(w, 400, "Image not modifiable")
					return
				}
				if _, ok := patch["group"]; ok {
					failure(w, 400, "group is immutable")
					return
				}
			}
		}
		for k, v := range patch {
			if k == "metadata" && value[k] != nil {
				var old, new map[string]json.RawMessage
				_ = json.Unmarshal(value[k], &old)
				_ = json.Unmarshal(v, &new)
				for k, v := range new {
					old[k] = v
				}
				v, _ = json.Marshal(old)
			}
			value[k] = v
		}
		value["id"], _ = json.Marshal(id)
		value["type"], _ = json.Marshal(kind)
		raw, _ := json.Marshal(value)
		if kind == "scene" {
			var scene hue.Scene
			_ = json.Unmarshal(raw, &scene)
			if scene.Speed == nil {
				v := 1.0
				scene.Speed = &v
			}
			if scene.AutoDynamic == nil {
				v := false
				scene.AutoDynamic = &v
			}
			if scene.Palette == nil {
				scene.Palette = json.RawMessage(`{"color":[],"dimming":[],"color_temperature":[]}`)
			}
			for i, item := range scene.Actions {
				var light hue.Light
				if err := json.Unmarshal(b.resources["light"][item.Target.RID], &light); err != nil {
					failure(w, 400, "unknown light")
					return
				}
				if item.Action.Color != nil {
					if light.Color == nil {
						failure(w, 400, "light does not support color")
						return
					}
					scene.Actions[i].Action.Color.XY = colors.Round(colors.Clip(item.Action.Color.XY, colors.Gamut(light.Color.GamutType)))
				}
				if item.Action.ColorTemperature != nil {
					if light.ColorTemperature == nil {
						failure(w, 400, "light does not support color temperature")
						return
					}
					scene.Actions[i].Action.ColorTemperature.Mirek = colors.ClampMirek(item.Action.ColorTemperature.Mirek, light.ColorTemperature.MirekSchema.Min, light.ColorTemperature.MirekSchema.Max)
				}
			}
			raw, _ = json.Marshal(scene)
		}
		items[id] = raw
		success(w, []hue.Reference{{RID: id, RType: kind}})
	case "DELETE":
		if id == "" {
			failure(w, 405, "ID required")
			return
		}
		if _, ok := items[id]; !ok {
			failure(w, 404, "not found")
			return
		}
		delete(items, id)
		success(w, []hue.Reference{{RID: id, RType: kind}})
	default:
		failure(w, 405, "method not allowed")
	}
}
