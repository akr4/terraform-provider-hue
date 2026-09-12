package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/akr4/terraform-provider-hue/internal/pull"
)

type importCreation struct {
	Address, ID, Path string
	Source            []byte
}
type importBlocksPlan struct {
	NeedsConfig   bool
	ModuleImports bool
	ImportFiles   []importCreation
	Changes       []*pull.Change
	Creates       []importCreation
	Skipped       []string
	Problems      []string
}

func terraformCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "terraform", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("terraform %s failed: %s", strings.Join(args, " "), stderr.String())
	}
	return data, nil
}
func stateReader(deps dependencies) func(context.Context) ([]byte, error) {
	if deps.readState != nil {
		return deps.readState
	}
	return func(ctx context.Context) ([]byte, error) {
		run := deps.terraform
		if run == nil {
			run = terraformCommand
		}
		// A successful but empty state pull occurs in a fresh workspace. Confirm
		// it with show; never hide backend/authentication failures as empty state.
		s, err := run(ctx, "state", "pull")
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(s)) > 0 {
			return s, nil
		}
		shown, e := run(ctx, "show", "-json")
		if e != nil {
			return nil, e
		}
		var v map[string]json.RawMessage
		if json.Unmarshal(shown, &v) == nil && v["values"] == nil {
			return []byte(`{"resources":[]}`), nil
		}
		return nil, fmt.Errorf("cannot read Terraform state: %v", err)
	}
}
func fetchPullResources(ctx context.Context, client *hue.Client) (map[string]map[string]json.RawMessage, error) {
	result := map[string]map[string]json.RawMessage{}
	for _, kind := range []string{"room", "zone", "scene", "smart_scene", "behavior_instance"} {
		var items []json.RawMessage
		if err := client.Get(ctx, "/clip/v2/resource/"+kind, &items); err != nil {
			return nil, err
		}
		result[kind] = map[string]json.RawMessage{}
		for _, raw := range items {
			var r hue.Group
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			if r.ID == "" {
				return nil, fmt.Errorf("bridge returned an empty UUID")
			}
			if result[kind][r.ID] != nil {
				return nil, fmt.Errorf("bridge returned duplicate UUID %s", r.ID)
			}
			result[kind][r.ID] = raw
		}
	}
	return result, nil
}

func prepareImportBlocks(state []byte, remote map[string]map[string]json.RawMessage, opts pullNewOptions) (*importBlocksPlan, error) {
	resources, err := pull.ManagedResources(state)
	if err != nil {
		return nil, err
	}
	modules, err := pull.ConfigModules(".")
	if err != nil {
		return nil, err
	}
	if err := pull.CheckProviderEnvironment(modules, os.Getenv("HUE_BRIDGE_HOST"), os.Getenv("HUE_BRIDGE_APPLICATION_KEY")); err != nil {
		return nil, err
	}
	scope := strings.TrimSuffix(opts.module, ".")
	destination, ok := modules[scope]
	if !ok {
		return nil, fmt.Errorf("module %s was not found", scope)
	}
	plan := &importBlocksPlan{}
	pending, e := pull.PendingImports(modules, resources)
	if e != nil {
		return nil, e
	}
	for _, r := range pending {
		k, n, _ := pull.ResourceAddress(r.Address)
		exists, e := pull.HasResourceDefinition(modules[pull.ModuleAddress(r.Address)], k, n)
		if e != nil {
			return nil, e
		}
		if !exists {
			plan.NeedsConfig = true
			if pull.ModuleAddress(r.Address) != "" {
				plan.ModuleImports = true
			}
		}
		resources = append(resources, r)
	}
	known := map[string]bool{}
	selectors := opts.selectors
	if len(selectors) == 0 && opts.id != "" {
		selectors = []string{opts.id}
	}
	matched := map[string]bool{}
	selectResource := func(id, address string) bool {
		selected := len(selectors) == 0
		for _, selector := range selectors {
			if selector == id || (address != "" && selector == address) {
				matched[selector] = true
				selected = true
			}
		}
		return selected
	}
	for _, r := range resources {
		known[r.ID] = true
	}
	reserved := map[string]bool{}
	for _, r := range resources {
		reserved[r.Address] = true
	}
	for _, kind := range []string{"room", "zone", "scene", "smart_scene", "behavior_instance"} {
		ids := []string{}
		for id := range remote[kind] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if known[id] || !selectResource(id, "") {
				continue
			}
			raw := remote[kind][id]
			var item hue.Group
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, err
			}
			name := pull.ResourceName(item.Metadata.Name)
			if opts.address != "" {
				k, n, e := pull.ResourceAddress(opts.address)
				if e != nil {
					return nil, e
				}
				if k != kind {
					plan.Problems = append(plan.Problems, "resource type does not match import address")
					continue
				}
				name = n
			}
			addr := opts.module + "hue_" + kind + "." + name
			exists, pathErr := pull.HasResourceDefinition(destination, kind, name)
			if pull.AddressReserved(state, kind, name, scope) || reserved[addr] || exists || pathErr != nil {
				// Batch import must accommodate repeated Hue scene names without
				// silently overwriting a declaration. UUIDs make the suffix stable.
				name += "_" + strings.ReplaceAll(id, "-", "_")
				addr = opts.module + "hue_" + kind + "." + name
				exists, pathErr = pull.HasResourceDefinition(destination, kind, name)
			}
			if exists || pathErr != nil || pull.AddressReserved(state, kind, name, scope) || reserved[addr] {
				plan.Problems = append(plan.Problems, addr+": destination is reserved or cannot be parsed")
				continue
			}
			reserved[addr] = true

			plan.Creates = append(plan.Creates, importCreation{Address: addr, ID: id})
			plan.NeedsConfig = true
			if scope != "" {
				plan.ModuleImports = true
			}
		}
	}
	// Managed and pending resources are only inventory entries. Terraform owns
	// their definitions, including local edits and resources absent from Bridge.
	for _, r := range resources {
		if len(selectors) > 0 && selectResource(r.ID, r.Address) {
			plan.Skipped = append(plan.Skipped, r.Address+" ["+r.ID+"]: already managed or pending import")
		}
	}
	for _, selector := range selectors {
		if !matched[selector] {
			plan.Problems = append(plan.Problems, "resource not found in selected scope: "+selector)
		}
	}

	if len(plan.Creates) > 0 {
		path := filepath.Join(modules[""], "imports_hue.tf")
		var source strings.Builder
		for _, c := range plan.Creates {
			fmt.Fprintf(&source, "import {\n  to = %s\n  id = %q\n}\n\n", c.Address, c.ID)
		}
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			plan.ImportFiles = append(plan.ImportFiles, importCreation{Path: path, Source: []byte(source.String())})
		} else if e != nil || !info.Mode().IsRegular() {
			plan.Problems = append(plan.Problems, "import file is inaccessible or not a regular file: "+path)
		} else {
			original, e := os.ReadFile(path)
			if e != nil {
				return nil, e
			}
			plan.Changes = append(plan.Changes, &pull.Change{Path: path, Original: original, Edits: []pull.Edit{{Start: len(original), End: len(original), After: "\n" + source.String(), Field: "import blocks"}}})
			plan.Changes, e = pull.CombineChanges(plan.Changes)
			if e != nil {
				return nil, e
			}
		}
	}

	return plan, nil
}

func importBlocks(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	opts, err := parsePullOptions(args, true)
	// Each positional argument selects an existing address or UUID.
	if err != nil || opts.address != "" {
		return fmt.Errorf("usage: hue-tf import-blocks [UUID | RESOURCE_ADDRESS ...] [--module NAME[.NAME...]] [--write]")
	}
	return importBlocksOptions(ctx, opts, out, deps)
}

func importBlocksOptions(ctx context.Context, opts pullNewOptions, out io.Writer, deps dependencies) error {

	key := os.Getenv("HUE_BRIDGE_APPLICATION_KEY")
	if key == "" {
		return fmt.Errorf("HUE_BRIDGE_APPLICATION_KEY is required")
	}
	if _, err := os.Stat(".hue-pull-transaction"); err == nil {
		return fmt.Errorf("an interrupted pull needs recovery; inspect .hue-pull-transaction before retrying")
	} else if !os.IsNotExist(err) {
		return err
	}
	readState := stateReader(deps)
	state, err := readState(ctx)
	if err != nil {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	remote, err := fetchPullResources(ctx, client)
	if err != nil {
		return err
	}
	plan, err := prepareImportBlocks(state, remote, opts)
	if err != nil {
		return err
	}
	for _, c := range plan.Changes {
		for _, e := range c.Edits {
			fmt.Fprintf(out, "%s: %s: %s -> %s\n", c.Path, e.Field, e.Before, e.After)
		}
	}
	for _, c := range plan.Creates {
		fmt.Fprintf(out, "Prepare import %s [%s]\n", c.Address, c.ID)
	}
	for _, c := range plan.ImportFiles {
		fmt.Fprintf(out, "Import block: %s\n%s\n", c.Path, c.Source)
	}
	for _, skipped := range plan.Skipped {
		fmt.Fprintf(out, "Skip: %s\n", skipped)
	}
	fmt.Fprintf(out, "Import blocks: %d new, %d blocked.\n", len(plan.Creates), len(plan.Problems))
	for _, problem := range plan.Problems {
		fmt.Fprintf(out, "Blocked: %s\n", problem)
	}
	if len(plan.Problems) > 0 {
		return fmt.Errorf("import-blocks is blocked; no files or state were changed")
	}
	if !opts.write {
		fmt.Fprintln(out, "Preview only. Use --write to prepare import blocks. Neither bridge nor state is changed.")
		printPullNext(out, plan)
		return nil
	}
	// Detect competing state/config edits before starting a recoverable transaction.
	current, err := readState(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(state, current) {
		return fmt.Errorf("Terraform state changed during preview; retry")
	}
	return writeImportBlocks(ctx, plan, out, deps)
}

func writeImportBlocks(ctx context.Context, plan *importBlocksPlan, out io.Writer, deps dependencies) (err error) {
	run := deps.terraform
	if run == nil {
		run = terraformCommand
	}
	if len(plan.ImportFiles) == 0 && len(plan.Changes) == 0 {
		fmt.Fprintln(out, "No import blocks to add.")
		return nil
	}
	transaction, err := filepath.Abs(".hue-pull-transaction")
	if err != nil {
		return err
	}
	if err = os.Mkdir(transaction, 0700); err != nil {
		return err
	}
	success := false
	newFiles := plan.ImportFiles
	var writtenChanges []*pull.Change
	var writtenCreates []importCreation
	defer func() {
		if !success {
			rollbackErr := rollbackPullFiles(writtenChanges, writtenCreates)
			if rollbackErr == nil {
				_ = os.RemoveAll(transaction)
				fmt.Fprintln(out, "Import block generation stopped; file edits were rolled back. State was not changed.")
				return
			}
			fmt.Fprintf(out, "File rollback needs attention: %v\n", rollbackErr)
		}
		if success {
			_ = os.RemoveAll(transaction)
		} else {
			fmt.Fprintf(out, "Import block generation interrupted. Recovery data: %s. Files may be partially updated; neither bridge nor state was changed.\n", transaction)
		}
	}()

	// Save file backups before editing; Terraform owns all state writes.
	manifest := struct {
		Changed map[string]string
		Created []string
		Imports []importCreation
	}{Changed: map[string]string{}, Imports: plan.Creates}
	for i, c := range plan.Changes {
		info, e := os.Lstat(c.Path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", c.Path)
		}
		actual, e := os.ReadFile(c.Path)
		if e != nil {
			return e
		}
		if !bytes.Equal(actual, c.Original) {
			return fmt.Errorf("file changed: %s", c.Path)
		}
		backup := filepath.Join(transaction, fmt.Sprintf("file-%d.tf", i))
		if err = os.WriteFile(backup, c.Original, 0600); err != nil {
			return err
		}
		manifest.Changed[c.Path] = backup
	}
	for _, c := range newFiles {
		if _, e := os.Lstat(c.Path); !os.IsNotExist(e) {
			return fmt.Errorf("new file already exists: %s", c.Path)
		}
		manifest.Created = append(manifest.Created, c.Path)
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(transaction, "manifest.json"), body, 0600); err != nil {
		return err
	}
	for _, c := range plan.Changes {
		if _, err = c.Write(); err != nil {
			return err
		}
		writtenChanges = append(writtenChanges, c)
	}
	for _, c := range newFiles {
		if err = pull.WriteNew(c.Path, c.Source); err != nil {
			return err
		}
		writtenCreates = append(writtenCreates, c)
	}
	if !plan.NeedsConfig {
		if _, err = run(ctx, "validate", "-no-color"); err != nil {
			return err
		}
	}
	success = true
	fmt.Fprintln(out, "Prepared import blocks. Neither bridge nor state was changed.")
	printPullNext(out, plan)
	return nil
}

func rollbackPullFiles(changes []*pull.Change, creates []importCreation) error {
	for _, c := range changes {
		info, err := os.Lstat(c.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("file replaced: %s", c.Path)
		}
		actual, err := os.ReadFile(c.Path)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, c.Updated) {
			return fmt.Errorf("file changed concurrently: %s", c.Path)
		}
		if err = os.WriteFile(c.Path, c.Original, info.Mode().Perm()); err != nil {
			return err
		}
	}
	for _, c := range creates {
		info, err := os.Lstat(c.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("file replaced: %s", c.Path)
		}
		actual, err := os.ReadFile(c.Path)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, c.Source) {
			return fmt.Errorf("file changed concurrently: %s", c.Path)
		}
		if err = os.Remove(c.Path); err != nil {
			return err
		}
	}
	return nil
}

func printPullNext(out io.Writer, plan *importBlocksPlan) {
	if plan.NeedsConfig {
		fmt.Fprintln(out, "Resource definitions are not generated by hue-tf. Next: terraform plan -generate-config-out=generated.tf (use a new file name)")
		if plan.ModuleImports {
			fmt.Fprintln(out, "Module import targets need resource definitions in their modules; Terraform config generation supports root resources only.")
		}
	} else {
		fmt.Fprintln(out, "Next: terraform plan")
	}
	fmt.Fprintln(out, "Review the configuration and plan, then run terraform apply.")
}
