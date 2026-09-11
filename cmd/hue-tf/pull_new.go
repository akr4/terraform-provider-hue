package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func pullNew(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	opts, err := parsePullNewArgs(args)
	if err != nil {
		return err
	}
	scope := ""
	if opts.address != "" {
		if _, _, err := pull.ResourceAddress(opts.address); err != nil {
			return err
		}
		scope = pull.ModuleAddress(opts.address)
		if scope != "" {
			opts.module = scope + "."
		}
	}
	if opts.id != "" {
		return pullBatchOptions(ctx, opts, out, deps)
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
	managed, _, err := pull.Inventory(data, scope)
	if err != nil {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	if opts.id == "" {
		count := 0
		for _, resourceKind := range []string{"room", "zone", "scene", "smart_scene", "behavior_instance"} {
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
			if resourceKind == "scene" || resourceKind == "smart_scene" {
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
			fmt.Fprintln(out, "Prepare import blocks: hue-tf pull --new RESOURCE_UUID [--module MODULE]")
		}
		return nil
	}
	return nil
}

// Module selectors are names relative to the root, not source directory paths.
type pullNewOptions struct {
	id, address, module string
	write               bool
}

func parsePullNewArgs(args []string) (pullNewOptions, error) {
	return parsePullOptions(args, false)
}

func parsePullOptions(args []string, allowAll bool) (pullNewOptions, error) {
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
	if len(positional) > 2 || (!allowAll && len(positional) == 0 && (o.write || seenModule)) || (len(positional) == 2 && seenModule) {
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

func pullResourceName(name string) string { return pull.ResourceName(name) }
