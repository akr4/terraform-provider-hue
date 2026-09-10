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

func pullNew(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	if len(args) != 0 && len(args) != 2 && !(len(args) == 3 && args[2] == "--write") {
		return fmt.Errorf("usage: hue-tf pull --new [RESOURCE_UUID hue_TYPE.NAME [--write]]")
	}
	name, kind := "", ""
	if len(args) > 0 {
		var err error
		kind, name, err = pull.ResourceAddress(args[1])
		if err != nil {
			return err
		}
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
		return fmt.Errorf("cannot read Terraform state; use the existing Terraform directory and workspace")
	}
	managed, groups, err := pull.Inventory(data)
	if err != nil {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	var resources []json.RawMessage
	if len(args) == 0 {
		count := 0
		for _, resourceKind := range []string{"room", "zone", "scene"} {
			var items []json.RawMessage
			if err = client.Get(ctx, "/clip/v2/resource/"+resourceKind, &items); err != nil {
				return err
			}
			var unmanaged []json.RawMessage
			for _, raw := range items {
				var item hue.Group
				if err = json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if !managed[item.ID] {
					unmanaged = append(unmanaged, raw)
				}
			}
			if len(unmanaged) == 0 {
				continue
			}
			count += len(unmanaged)
			fmt.Fprintf(out, "%s:\n", resourceKind)
			if resourceKind == "scene" {
				if err = printScenes(ctx, client, unmanaged, out); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(out, "ID\tNAME")
				for _, raw := range unmanaged {
					var g hue.Group
					if err = json.Unmarshal(raw, &g); err != nil {
						return err
					}
					fmt.Fprintf(out, "%s\t%s\n", tableText(g.ID), tableText(g.Metadata.Name))
				}
			}
		}
		if count == 0 {
			fmt.Fprintln(out, "No unmanaged resources.")
		} else {
			fmt.Fprintln(out, "Preview a definition: hue-tf pull --new RESOURCE_UUID hue_TYPE.NAME")
		}
		return nil
	}
	if err = client.Get(ctx, "/clip/v2/resource/"+kind, &resources); err != nil {
		return err
	}

	if managed[args[0]] {
		return fmt.Errorf("resource is already imported; use pull with its existing resource address")
	}
	var selected json.RawMessage
	for _, raw := range resources {
		var s hue.Scene
		if err = json.Unmarshal(raw, &s); err != nil {
			return err
		}
		if s.ID == args[0] {
			selected = raw
		}
	}
	if selected == nil {
		return fmt.Errorf("resource UUID was not found for the selected type on the bridge")
	}
	if pull.AddressReserved(data, kind, name) {
		return fmt.Errorf("hue_%s.%s already exists in state; choose another name", kind, name)
	}
	path, err := pull.NewResourcePath(".", kind, name)
	if err != nil {
		return err
	}
	var src []byte
	if kind == "scene" {
		src, err = pull.NewScene(selected, name, groups)
	} else {
		src, err = pull.NewGroup(selected, kind, name)
	}
	if err != nil {
		return err
	}
	if len(args) == 3 {
		if err = pull.WriteNew(path, src); err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %s. Import before running plan or apply:\n", path)
	} else {
		fmt.Fprintf(out, "Preview: %s\n%s\nUse --write to create this file, then import:\n", path, src)
	}
	fmt.Fprintf(out, "terraform import %s %s\n", shellQuote(args[1]), shellQuote(args[0]))
	fmt.Fprintln(out, "After import, run terraform plan and check for No changes. No bridge or state changes were made.")
	return nil
}
