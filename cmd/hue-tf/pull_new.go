package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func pullNew(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	opts, err := parsePullNewArgs(args)
	if err != nil {
		return err
	}
	name, kind := "", ""
	dir, scope := ".", ""
	address := opts.address
	if address != "" {
		kind, name, err = pull.ResourceAddress(address)
		if err != nil {
			return err
		}
	}
	// Resolve the destination before contacting the bridge, including auto naming.
	if opts.id != "" {
		destination := address
		if destination == "" {
			destination = opts.module + "hue_scene.placeholder"
		}
		dir, err = pull.ModuleDir(".", destination)
		if err != nil {
			return err
		}
		scope = pull.ModuleAddress(destination)
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
	managed, groups, err := pull.Inventory(data, scope)
	if err != nil {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	var resources []json.RawMessage
	if opts.id == "" {
		count := 0
		for _, resourceKind := range []string{"room", "zone", "scene", "behavior_instance"} {
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
			fmt.Fprintln(out, "Preview a definition: hue-tf pull --new RESOURCE_UUID [--module MODULE]")
		}
		return nil
	}
	if managed[opts.id] {
		return fmt.Errorf("resource is already imported; use pull with its existing resource address")
	}
	kinds := []string{kind}
	if kind == "" {
		kinds = []string{"room", "zone", "scene", "behavior_instance"}
	}
	var selected json.RawMessage
	for _, candidate := range kinds {
		if err = client.Get(ctx, "/clip/v2/resource/"+candidate, &resources); err != nil {
			return err
		}
		for _, raw := range resources {
			var item hue.Group
			if err = json.Unmarshal(raw, &item); err != nil {
				return err
			}
			if item.ID != opts.id {
				continue
			}
			if selected != nil {
				return fmt.Errorf("resource UUID matched multiple resources; specify an explicit address")
			}
			selected, kind = append(json.RawMessage(nil), raw...), candidate
			if address == "" {
				name = pullResourceName(item.Metadata.Name)
			}
		}
	}
	if selected == nil {
		return fmt.Errorf("resource UUID was not found for the selected type on the bridge")
	}
	if address == "" {
		address = opts.module + "hue_" + kind + "." + name
		fmt.Fprintf(out, "Address: %s\n", address)
	}
	if pull.AddressReserved(data, kind, name, scope) {
		return fmt.Errorf("hue_%s.%s already exists in state; choose another name", kind, name)
	}
	path, err := pull.NewResourcePath(dir, kind, name)
	if err != nil {
		return err
	}
	var src []byte
	if kind == "scene" {
		src, err = pull.NewScene(selected, name, groups)
	} else if kind == "behavior_instance" {
		src, err = pull.NewBehavior(selected, name)
	} else {
		src, err = pull.NewGroup(selected, kind, name)
	}
	if err != nil {
		return err
	}
	if opts.write {
		if err = pull.WriteNew(path, src); err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %s. Import before running plan or apply:\n", path)
	} else {
		fmt.Fprintf(out, "Preview: %s\n%s\nUse --write to create this file, then import:\n", path, src)
	}
	fmt.Fprintf(out, "terraform import %s %s\n", shellQuote(address), shellQuote(opts.id))
	fmt.Fprintln(out, "After import, run terraform plan and check for No changes. No bridge or state changes were made.")
	return nil
}

// Module selectors are names relative to the root, not source directory paths.
type pullNewOptions struct {
	id, address, module string
	write               bool
}

func parsePullNewArgs(args []string) (pullNewOptions, error) {
	var o pullNewOptions
	usage := fmt.Errorf("usage: hue-tf pull --new [RESOURCE_UUID [hue_TYPE.NAME | --module NAME[.NAME...]] [--write]]")
	var positional []string
	seenModule := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--write":
			if o.write {
				return o, usage
			}
			o.write = true
		case "--module":
			if seenModule || i+1 == len(args) {
				return o, usage
			}
			seenModule = true
			i++
			for _, part := range strings.Split(args[i], ".") {
				if !hclsyntax.ValidIdentifier(part) {
					return o, usage
				}
				o.module += "module." + part + "."
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return o, usage
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) > 2 || (len(positional) == 0 && (o.write || seenModule)) || (len(positional) == 2 && seenModule) {
		return o, usage
	}
	if len(positional) > 0 {
		o.id = positional[0]
	}
	if len(positional) == 2 {
		o.address = positional[1]
	}
	return o, nil
}

func pullResourceName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	if name == "" {
		return "resource"
	}
	if !hclsyntax.ValidIdentifier(name) {
		name = "resource_" + name
	}
	return name
}
