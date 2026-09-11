package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/grandcat/zeroconf"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type dependencies struct {
	terraform func(context.Context, ...string) ([]byte, error)
	readState func(context.Context) ([]byte, error)
	newClient func(string, string) (*hue.Client, error)
	discover  func(context.Context) ([]string, error)
}

func run(ctx context.Context, args []string, out, errout io.Writer) error {
	return runWith(ctx, args, out, errout, dependencies{newClient: hue.NewClient, discover: discover})
}
func runWith(ctx context.Context, args []string, out, errout io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return errors.New("usage: hue-tf init | ls <room|zone|scene|light|device|switch|behavior_instance|behavior_script|button> [--json] | raw /clip/v2/<path> | pull [UUID | RESOURCE_ADDRESS] [--module NAME[.NAME...]] [--write]")
	}
	if args[0] == "pull" {
		return pullScene(ctx, args[1:], out, deps)
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return errors.New("usage: hue-tf init")
		}
		return initialize(ctx, out, errout, deps)
	}
	if args[0] != "raw" && args[0] != "ls" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	if len(args) < 2 || len(args) > 3 {
		return errors.New("expected a resource type or v2 path")
	}
	if args[0] == "raw" && len(args) != 2 {
		return errors.New("usage: hue-tf raw /clip/v2/<path>")
	}
	if args[0] == "ls" {
		switch args[1] {
		case "room", "zone", "scene", "light", "device", "switch", "behavior_instance", "behavior_script", "button":
		default:
			return errors.New("unsupported resource type")
		}
		if len(args) == 3 && args[2] != "--json" {
			return errors.New("unknown flag")
		}
	}
	host, key := os.Getenv("HUE_BRIDGE_HOST"), os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if key == "" {
		return errors.New("HUE_BRIDGE_APPLICATION_KEY is required; run hue-tf init")
	}
	client, err := deps.newClient(host, key)
	if err != nil {
		return err
	}
	if args[0] == "raw" {
		data, err := client.Raw(ctx, args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	if args[1] == "switch" {
		return listSwitches(ctx, client, out, len(args) == 3)
	}
	var data []json.RawMessage
	if err := client.Get(ctx, "/clip/v2/resource/"+args[1], &data); err != nil {
		return err
	}
	if len(args) == 3 {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	if args[1] == "scene" {
		return printScenes(ctx, client, data, out)
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME")
	for _, raw := range data {
		var item struct {
			ID       string       `json:"id"`
			Metadata hue.Metadata `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		fmt.Fprintf(table, "%s\t%s\n", item.ID, tableText(item.Metadata.Name))
	}
	return table.Flush()
}

// Scene names repeat across rooms and zones. Resolve each referenced group type
// once, then show the group UUID too so duplicate group names remain distinct.
func printScenes(ctx context.Context, client *hue.Client, data []json.RawMessage, out io.Writer) error {
	scenes := make([]hue.Scene, 0, len(data))
	needed := map[string]bool{}
	for _, raw := range data {
		var scene hue.Scene
		if err := json.Unmarshal(raw, &scene); err != nil {
			return err
		}
		scenes = append(scenes, scene)
		needed[scene.Group.RType] = true
	}
	names := map[hue.Reference]string{}
	for _, kind := range []string{"room", "zone"} {
		if !needed[kind] {
			continue
		}
		var groups []hue.Group
		if err := client.Get(ctx, "/clip/v2/resource/"+kind, &groups); err != nil {
			return fmt.Errorf("read scene %s names: %w", kind, err)
		}
		for _, group := range groups {
			names[hue.Reference{RID: group.ID, RType: kind}] = group.Metadata.Name
		}
	}
	sort.Slice(scenes, func(i, j int) bool {
		a, b := scenes[i], scenes[j]
		if names[a.Group] != names[b.Group] {
			return names[a.Group] < names[b.Group]
		}
		if a.Group.RType != b.Group.RType {
			return a.Group.RType < b.Group.RType
		}
		if a.Group.RID != b.Group.RID {
			return a.Group.RID < b.Group.RID
		}
		if a.Metadata.Name != b.Metadata.Name {
			return a.Metadata.Name < b.Metadata.Name
		}
		return a.ID < b.ID
	})
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tGROUP TYPE\tGROUP NAME\tGROUP ID")
	for _, scene := range scenes {
		name, ok := names[scene.Group]
		if !ok {
			name = "(unknown)"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", tableText(scene.ID), tableText(scene.Metadata.Name), tableText(scene.Group.RType), tableText(name), tableText(scene.Group.RID))
	}
	return table.Flush()
}
func tableText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
}

func discover(ctx context.Context) ([]string, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 16)
	if err := resolver.Browse(ctx, "_hue._tcp", "local.", entries); err != nil {
		return nil, err
	}
	hosts := map[string]bool{}
	for {
		select {
		case entry, ok := <-entries:
			if !ok {
				return sortedHosts(hosts), nil
			}
			for _, ip := range entry.AddrIPv4 {
				hosts[ip.String()] = true
			}
			if len(entry.AddrIPv4) > 0 {
				continue
			}
			for _, ip := range entry.AddrIPv6 {
				if !ip.IsLinkLocalUnicast() {
					hosts[ip.String()] = true
				}
			}
		case <-ctx.Done():
			return sortedHosts(hosts), nil
		}
	}
}
func sortedHosts(hosts map[string]bool) []string {
	result := []string{}
	for host := range hosts {
		if net.ParseIP(host) != nil {
			result = append(result, host)
		}
	}
	sort.Strings(result)
	return result
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func initialize(ctx context.Context, out, errout io.Writer, deps dependencies) error {
	host := os.Getenv("HUE_BRIDGE_HOST")
	if host == "" {
		hosts, err := deps.discover(ctx)
		if err != nil {
			return err
		}
		if len(hosts) == 0 {
			return errors.New("no bridge discovered; set HUE_BRIDGE_HOST to its IP address")
		}
		if len(hosts) > 1 {
			return fmt.Errorf("multiple bridge addresses found (%s); set HUE_BRIDGE_HOST to select one", strings.Join(hosts, ", "))
		}
		host = hosts[0]
	}
	client, err := deps.newClient(host, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(errout, "Press the link button on bridge %s within 60 seconds.\n", host)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		key, pending, err := client.Register(ctx)
		if err != nil {
			return err
		}
		if !pending {
			_, err = fmt.Fprintf(out, "export HUE_BRIDGE_HOST=%s\nexport HUE_BRIDGE_APPLICATION_KEY=%s\n", shellQuote(host), shellQuote(key))
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
