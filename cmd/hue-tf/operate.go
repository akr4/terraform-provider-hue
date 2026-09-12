package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var operationUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

// operate sends only the requested runtime action. It never reads Terraform
// state or changes saved scene configuration.
func operate(ctx context.Context, command string, args []string, out io.Writer, deps dependencies) error {
	usage := "usage: hue-tf identify DEVICE_UUID_OR_LIGHT_UUID"
	if command == "recall" {
		usage = "usage: hue-tf recall SCENE_UUID [--action active|dynamic_palette|static|activate|deactivate]"
	}
	id, action := "", ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--action" && command == "recall" {
			if action != "" || i+1 == len(args) {
				return fmt.Errorf("%s", usage)
			}
			i++
			action = args[i]
			switch action {
			case "active", "dynamic_palette", "static", "activate", "deactivate":
			default:
				return fmt.Errorf("%s", usage)
			}
		} else if id == "" && operationUUID.MatchString(args[i]) {
			id = strings.ToLower(args[i])
		} else {
			return fmt.Errorf("%s", usage)
		}
	}
	if id == "" {
		return fmt.Errorf("%s", usage)
	}
	key := os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if key == "" {
		return fmt.Errorf("HUE_BRIDGE_APPLICATION_KEY is required; run hue-tf init")
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	var data []json.RawMessage
	if err = client.Get(ctx, "/clip/v2/resource", &data); err != nil {
		return err
	}
	resources := map[string]detailResource{}
	for _, raw := range data {
		var r detailResource
		if err = json.Unmarshal(raw, &r); err != nil {
			return err
		}
		r.Raw = raw
		if _, exists := resources[r.ID]; exists {
			return fmt.Errorf("bridge returned duplicate resource %s", r.ID)
		}
		resources[r.ID] = r
	}
	target, ok := resources[id]
	if !ok {
		return fmt.Errorf("resource %s not found", id)
	}
	var payload any
	if command == "recall" {
		switch target.Type {
		case "scene":
			if action == "" {
				action = "active"
			}
			if action != "active" && action != "dynamic_palette" && action != "static" {
				return fmt.Errorf("scene supports active, dynamic_palette or static, not %s", action)
			}
		case "smart_scene":
			if action == "" {
				action = "activate"
			}
			if action != "activate" && action != "deactivate" {
				return fmt.Errorf("smart_scene supports activate or deactivate, not %s", action)
			}
		default:
			return fmt.Errorf("%s is %s; recall requires scene or smart_scene", id, target.Type)
		}
		payload = map[string]any{"recall": map[string]string{"action": action}}
	} else {
		if target.Type == "light" {
			owner, exists := resources[target.Owner.RID]
			if target.Owner.RType != "device" || !exists || owner.Type != "device" {
				return fmt.Errorf("light %s has no resolvable owning device", id)
			}
			target = owner
		}
		if target.Type != "device" {
			return fmt.Errorf("%s is %s; identify requires device or light", id, target.Type)
		}
		var properties map[string]json.RawMessage
		if err = json.Unmarshal(target.Raw, &properties); err != nil {
			return err
		}
		if value := properties["identify"]; len(value) == 0 || string(value) == "null" {
			return fmt.Errorf("device %s does not advertise identify support", target.ID)
		}
		action = "identify"
		payload = map[string]any{"identify": map[string]string{"action": "identify"}}
	}
	if !operationUUID.MatchString(target.ID) {
		return fmt.Errorf("bridge returned invalid target UUID")
	}
	if err = client.Update(ctx, target.Type, target.ID, payload); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Requested %s: %s / %s (%s)\n", action, target.Type, tableText(target.Metadata.Name), target.ID)
	return err
}
