package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
)

func pullScene(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	if len(args) > 0 && args[0] == "--new" {
		return pullNew(ctx, args[1:], out, deps)
	}
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "--write") {
		return fmt.Errorf("usage: hue-tf pull hue_room.NAME|hue_zone.NAME|hue_scene.NAME [--write]")
	}
	address := args[0]
	kind, name, err := pull.ResourceAddress(address)
	if err != nil {
		return err
	}

	dir, err := pull.ModuleDir(".", address)
	if err != nil {
		return err
	}

	key := os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if key == "" {
		return fmt.Errorf("HUE_BRIDGE_APPLICATION_KEY is required")
	}
	readState := deps.readState
	if readState == nil {
		readState = func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "terraform", "state", "pull").Output()
		}
	}
	data, err := readState(ctx)
	if err != nil {
		return fmt.Errorf("cannot read Terraform state; check the current directory, workspace and backend")
	}
	baseline, err := pull.State(data, address)
	if err != nil {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	var change *pull.Change
	if kind == "scene" {
		raw, e := hue.GetOne[json.RawMessage](ctx, client, kind, baseline.ID)
		if e != nil {
			return e
		}
		scene, e := pull.DecodeScene(raw)
		if e != nil {
			return e
		}
		change, err = pull.Prepare(dir, name, baseline, scene)
	} else {
		group, e := hue.GetOne[hue.Group](ctx, client, kind, baseline.ID)
		if e != nil {
			return e
		}
		change, err = pull.PrepareGroup(dir, kind, name, baseline, group)
	}
	if err != nil {
		return err
	}
	if kind == "scene" {
		fmt.Fprintln(out, "Scope: on, brightness, mirek/kelvin, color_xy and temperature/color mode switches; other scene attributes are not synchronized.")
	} else {
		fmt.Fprintln(out, "Scope: name, archetype and literal children membership.")
	}
	for _, e := range change.Edits {
		field := e.Field
		if kind == "scene" {
			field = fmt.Sprintf("actions[%q].%s", e.Light, e.Field)
		}
		fmt.Fprintf(out, "%s: %s.%s: %s -> %s\n", change.Path, address, field, e.Before, e.After)
	}
	if len(change.Edits) == 0 {
		fmt.Fprintln(out, "No supported resource changes.")
		return nil
	}

	if len(args) == 1 {
		fmt.Fprintln(out, "Preview only. Use --write to update the .tf file; the bridge and Terraform state are never changed.")
		return nil
	}
	backup, err := change.Write()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Updated %s (backup: %s). Review the file diff, then run terraform plan.\n", change.Path, backup)
	return nil
}
