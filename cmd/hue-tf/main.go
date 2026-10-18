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
	"runtime/debug"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/grandcat/zeroconf"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

const usageText = `Usage: hue-tf COMMAND [ARGUMENTS]

Commands:
  init                     Create an application key; press the bridge's link button
  discover                 Find bridges on the local network and print their addresses and IDs
  ls TYPE [--json]         List room, zone, scene, smart_scene, light, device, switch,
                           behavior_instance, behavior_script or button resources
  show SCENE_UUID          Show a scene and what references it
  recall SCENE_UUID [--action ACTION]
                           Activate a scene
  identify UUID [--count N]
                           Make a device or light signal
  raw /clip/v2/PATH        Print the response of an API v2 GET request
  preview [PLAN_JSON|-] [--html] [--all] [--output FILE]
                           Render scene colors from a Terraform plan
  import-blocks [UUID | RESOURCE_ADDRESS ...] [--module NAME[.NAME...]] [--write]
                           Generate import blocks for unmanaged resources
  migrate [DIR ...] [--write]
                           Rewrite configurations for provider 0.2.0
  version                  Print the version

Commands that contact the bridge read HUE_BRIDGE_HOST, HUE_BRIDGE_ID and
HUE_BRIDGE_APPLICATION_KEY. hue-tf init prints all three.
`

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type dependencies struct {
	stdin     io.Reader
	wait      func(context.Context, time.Duration) error
	terraform func(context.Context, ...string) ([]byte, error)
	readState func(context.Context) ([]byte, error)
	newClient func(host, key, bridgeID string) (*hue.Client, error)
	discover  func(context.Context) ([]bridge, error)
	// observe reads the bridge ID from a bridge's certificate on first contact.
	observe func(ctx context.Context, host string) (string, error)
	// linkTimeout bounds how long init waits for the link button; zero means 60 seconds.
	linkTimeout time.Duration
}

func run(ctx context.Context, args []string, out, errout io.Writer) error {
	return runWith(ctx, args, out, errout, dependencies{newClient: hue.NewClient, discover: discover, observe: hue.ObserveBridgeID})
}
func runWith(ctx context.Context, args []string, out, errout io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return errors.New(strings.TrimSpace(usageText))
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, err := fmt.Fprint(out, usageText)
		return err
	case "version", "--version":
		_, err := fmt.Fprintln(out, "hue-tf", versionString())
		return err
	}
	if args[0] == "preview" {
		return previewCommand(args[1:], deps.stdin, out)
	}
	if args[0] == "pull" {
		return errors.New("pull was replaced by import-blocks; existing resource synchronization has been removed")
	}
	if args[0] == "import-blocks" {
		return importBlocksCommand(ctx, args[1:], out, deps)
	}
	if args[0] == "migrate" {
		return migrateCommand(args[1:], out)
	}
	if args[0] == "recall" || args[0] == "identify" {
		return operate(ctx, args[0], args[1:], out, deps)
	}
	if args[0] == "show" {
		return showScene(ctx, args[1:], out, deps)
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return errors.New("usage: hue-tf init")
		}
		return initialize(ctx, out, errout, deps)
	}
	if args[0] == "discover" {
		if len(args) != 1 {
			return errors.New("usage: hue-tf discover")
		}
		bridges, err := deps.discover(ctx)
		if err != nil {
			return err
		}
		if len(bridges) == 0 {
			return errors.New("no bridge discovered on the local network")
		}
		table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "ADDRESS\tBRIDGE ID")
		for _, b := range bridges {
			id := b.ID
			if id == "" {
				id = "-"
			}
			fmt.Fprintf(table, "%s\t%s\n", b.Host, id)
		}
		return table.Flush()
	}
	if args[0] != "raw" && args[0] != "ls" {
		return fmt.Errorf("unknown command %q; run hue-tf --help", args[0])
	}
	if len(args) < 2 || len(args) > 3 {
		return errors.New("expected a resource type or v2 path")
	}
	if args[0] == "raw" && len(args) != 2 {
		return errors.New("usage: hue-tf raw /clip/v2/<path>")
	}
	if args[0] == "ls" {
		switch args[1] {
		case "room", "zone", "scene", "smart_scene", "light", "device", "switch", "behavior_instance", "behavior_script", "button":
		default:
			return errors.New("unsupported resource type")
		}
		if len(args) == 3 && args[2] != "--json" {
			return errors.New("unknown flag")
		}
	}
	client, err := clientFromEnv(deps)
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
	if args[1] == "scene" || args[1] == "smart_scene" {
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

// bridge is an address found by mDNS, with the bridge ID that it advertised.
type bridge struct{ Host, ID string }

func discover(ctx context.Context) ([]bridge, error) {
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
	found := map[string]string{}
	for {
		select {
		case entry, ok := <-entries:
			if !ok {
				return sortedBridges(found), nil
			}
			id := ""
			for _, txt := range entry.Text {
				if value, ok := strings.CutPrefix(txt, "bridgeid="); ok {
					id, _ = hue.ParseBridgeID(value)
				}
			}
			for _, ip := range entry.AddrIPv4 {
				found[ip.String()] = id
			}
			if len(entry.AddrIPv4) > 0 {
				continue
			}
			for _, ip := range entry.AddrIPv6 {
				if !ip.IsLinkLocalUnicast() {
					found[ip.String()] = id
				}
			}
		case <-ctx.Done():
			return sortedBridges(found), nil
		}
	}
}
func sortedBridges(found map[string]string) []bridge {
	result := []bridge{}
	for host, id := range found {
		if net.ParseIP(host) != nil {
			result = append(result, bridge{Host: host, ID: id})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Host < result[j].Host })
	return result
}

// versionString falls back to the module version for go install builds.
func versionString() string {
	info, _ := debug.ReadBuildInfo()
	return buildVersion(version, info)
}

// buildVersion drops the module version's v, as release builds print 0.1.0.
func buildVersion(version string, info *debug.BuildInfo) string {
	if version != "dev" {
		return version
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return version
}

// clientFromEnv connects to the bridge named by the environment.
func clientFromEnv(deps dependencies) (*hue.Client, error) {
	host, id, key := os.Getenv("HUE_BRIDGE_HOST"), os.Getenv("HUE_BRIDGE_ID"), os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if key == "" {
		return nil, errors.New("HUE_BRIDGE_APPLICATION_KEY is not set; run hue-tf init")
	}
	if host == "" {
		return nil, errors.New("HUE_BRIDGE_HOST is not set; run hue-tf discover to find the bridge's address")
	}
	if id == "" {
		return nil, errors.New("HUE_BRIDGE_ID is not set; run hue-tf discover to find the bridge's ID")
	}
	return deps.newClient(host, key, id)
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func initialize(ctx context.Context, out, errout io.Writer, deps dependencies) error {
	host, id := os.Getenv("HUE_BRIDGE_HOST"), os.Getenv("HUE_BRIDGE_ID")
	if host == "" {
		bridges, err := deps.discover(ctx)
		if err != nil {
			return err
		}
		if len(bridges) == 0 {
			return errors.New("no bridge discovered; set HUE_BRIDGE_HOST to its IP address")
		}
		if len(bridges) > 1 {
			hosts := []string{}
			for _, b := range bridges {
				hosts = append(hosts, b.Host)
			}
			return fmt.Errorf("multiple bridge addresses found (%s); set HUE_BRIDGE_HOST to select one", strings.Join(hosts, ", "))
		}
		host = bridges[0].Host
		if id == "" {
			id = bridges[0].ID
		}
	}
	if id == "" {
		// First contact: trust the ID in the certificate the bridge presents,
		// which must still chain to the Hue root CA.
		observed, err := deps.observe(ctx, host)
		if err != nil {
			return fmt.Errorf("cannot read the bridge ID from %s: %w", host, err)
		}
		id = observed
	}
	client, err := deps.newClient(host, "", id)
	if err != nil {
		return err
	}
	timeout := deps.linkTimeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	fmt.Fprintf(errout, "Press the link button on bridge %s within %v.\n", host, timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	reached := false
	expired := func(err error) error {
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if reached {
			return fmt.Errorf("the link button on bridge %s was not pressed within %v; run hue-tf init again", host, timeout)
		}
		return fmt.Errorf("could not reach bridge %s within %v: %w", host, timeout, err)
	}
	for {
		key, pending, err := client.Register(ctx)
		if err != nil {
			return expired(err)
		}
		reached = true
		if !pending {
			_, err = fmt.Fprintf(out, "export HUE_BRIDGE_HOST=%s\nexport HUE_BRIDGE_ID=%s\nexport HUE_BRIDGE_APPLICATION_KEY=%s\n", shellQuote(host), shellQuote(id), shellQuote(key))
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return expired(ctx.Err())
		case <-timer.C:
		}
	}
}
