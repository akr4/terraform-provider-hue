package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	huecolor "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func showScene(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	if len(args) != 1 || !regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`).MatchString(args[0]) {
		return fmt.Errorf("usage: hue-tf show SCENE_UUID")
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
	return printSceneDetails(data, strings.ToLower(args[0]), out)
}

type detailResource struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Metadata hue.Metadata    `json:"metadata"`
	Owner    hue.Reference   `json:"owner"`
	Children []hue.Reference `json:"children"`
	Raw      json.RawMessage `json:"-"`
}

func printSceneDetails(data []json.RawMessage, id string, out io.Writer) error {
	resources := map[string]detailResource{}
	for _, raw := range data {
		var r detailResource
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("read resource: %w", err)
		}
		r.Raw = raw
		resources[r.ID] = r
	}
	target, ok := resources[id]
	if !ok {
		return fmt.Errorf("scene %s not found in v2 resources", id)
	}
	if target.Type != "scene" && target.Type != "smart_scene" {
		return fmt.Errorf("%s is %s; show supports scene and smart_scene", id, target.Type)
	}
	name := func(ref hue.Reference) string {
		r, ok := resources[ref.RID]
		if !ok || r.Type != ref.RType || r.Metadata.Name == "" {
			return "(unknown)"
		}
		return tableText(r.Metadata.Name)
	}
	var scene hue.Scene
	if err := json.Unmarshal(target.Raw, &scene); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "%s: %s\nID: %s\nGroup: %s / %s (%s)\n", target.Type, tableText(target.Metadata.Name), id, scene.Group.RType, name(scene.Group), scene.Group.RID)
	fmt.Fprintln(w, "\nReferences (v2 only; direct references, including disabled automations):")
	ids := make([]string, 0, len(resources))
	for rid := range resources {
		ids = append(ids, rid)
	}
	sort.Strings(ids)
	count := 0
	for _, rid := range ids {
		if rid == id {
			continue
		}
		r := resources[rid]
		var value any
		if err := json.Unmarshal(r.Raw, &value); err != nil {
			return err
		}
		paths := referencePaths(value, hue.Reference{RID: id, RType: target.Type}, "$")
		for _, path := range paths {
			count++
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", tableText(r.Type), tableText(r.Metadata.Name), r.ID, tableText(path))
		}
	}
	if count == 0 {
		fmt.Fprintln(w, "  None found. This does not prove that the scene is unused.")
	}
	fmt.Fprintln(w, "Manual use and v1 rules are not inspected.")
	if target.Type == "smart_scene" {
		var smart hue.SmartScene
		if err := json.Unmarshal(target.Raw, &smart); err != nil {
			return err
		}
		fmt.Fprintf(w, "\nState: %s\nTransition: %d ms\n", tableText(smart.State), smart.TransitionDuration)
		fmt.Fprintln(w, "DAYS\tSTART\tSCENE\tSCENE ID")
		for _, day := range smart.WeekTimeslots {
			for _, slot := range day.Timeslots {
				start, err := hue.FormatSmartStart(slot.StartTime)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", tableText(strings.Join(day.Recurrence, ",")), start, name(slot.Target), slot.Target.RID)
			}
		}
	} else {
		fmt.Fprintln(w, "\nActions (hex is an approximation of xy; brightness shown separately):")
		fmt.Fprintln(w, "ROOM\tLIGHT\tLIGHT ID\tON\tBRIGHTNESS\tCOLOR / TEMPERATURE\tOTHER")
		for _, a := range scene.Actions {
			rooms := []string{}
			light := resources[a.Target.RID]
			for _, rid := range ids {
				r := resources[rid]
				if r.Type != "room" {
					continue
				}
				for _, child := range r.Children {
					if child == a.Target || (light.Owner.RID != "" && child == light.Owner) {
						rooms = append(rooms, tableText(r.Metadata.Name))
						break
					}
				}
			}
			room := strings.Join(rooms, ", ")
			if room == "" {
				room = "(unknown)"
			}
			on, brightness, color := "-", "-", "-"
			if a.Action.On != nil {
				on = fmt.Sprint(a.Action.On.On)
			}
			if a.Action.Dimming != nil {
				brightness = fmt.Sprintf("%g%%", a.Action.Dimming.Brightness)
			}
			colors := []string{}
			if a.Action.Color != nil {
				colors = append(colors, huecolor.XYToHex(a.Action.Color.XY))
			}
			if t := a.Action.ColorTemperature; t != nil && t.Mirek > 0 {
				colors = append(colors, fmt.Sprintf("%d K (%d mirek)", 1000000/t.Mirek, t.Mirek))
			}
			if len(colors) > 0 {
				color = strings.Join(colors, " / ")
			}
			extras := []string{}
			if len(a.Action.Gradient) > 0 && string(a.Action.Gradient) != "null" {
				extras = append(extras, "gradient")
			}
			if len(a.Action.Effects) > 0 && string(a.Action.Effects) != "null" {
				extras = append(extras, "effects")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", room, name(a.Target), tableText(a.Target.RID), on, brightness, color, strings.Join(extras, ", "))
		}
		if len(scene.Actions) == 0 {
			fmt.Fprintln(w, "  No actions.")
		}
	}
	return w.Flush()
}

// Retain exact JSON paths rather than guessing script-specific button gestures.
// Only typed Hue references count; an unrelated string containing the UUID does not.
func referencePaths(value any, target hue.Reference, path string) []string {
	result := []string{}
	switch v := value.(type) {
	case map[string]any:
		if v["rid"] == target.RID && v["rtype"] == target.RType {
			return []string{path}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result = append(result, referencePaths(v[k], target, fmt.Sprintf("%s[%q]", path, k))...)
		}
	case []any:
		for i, item := range v {
			result = append(result, referencePaths(item, target, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return result
}
