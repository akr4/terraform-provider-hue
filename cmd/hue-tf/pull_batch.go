package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type pullCheckpoint struct {
	Version   int                                   `json:"version"`
	Identity  string                                `json:"identity"`
	Resources map[string]map[string]json.RawMessage `json:"resources"`
}
type pullCreation struct {
	Address, ID, Path string
	Source            []byte
}
type batchPlan struct {
	Changes    []*pull.Change
	Creates    []pullCreation
	Removes    []pull.ManagedResource
	Targets    []string
	Checkpoint pullCheckpoint
	Problems   []string
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
func pullIdentity(state []byte) string {
	var s struct{ Lineage string }
	_ = json.Unmarshal(state, &s)
	root, _ := filepath.Abs(".")
	h := sha256.Sum256([]byte(root + "\x00" + os.Getenv("HUE_BRIDGE_HOST") + "\x00" + os.Getenv("TF_WORKSPACE") + "\x00" + os.Getenv("TF_DATA_DIR") + "\x00" + s.Lineage))
	return hex.EncodeToString(h[:])
}
func fetchPullResources(ctx context.Context, client *hue.Client) (map[string]map[string]json.RawMessage, error) {
	result := map[string]map[string]json.RawMessage{}
	for _, kind := range []string{"room", "zone", "scene", "behavior_instance"} {
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

func prepareBatch(state []byte, remote map[string]map[string]json.RawMessage, opts pullNewOptions, previous pullCheckpoint) (*batchPlan, error) {
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
	plan := &batchPlan{Checkpoint: pullCheckpoint{Version: 1, Identity: pullIdentity(state), Resources: map[string]map[string]json.RawMessage{}}}
	if previous.Version == 1 && previous.Identity == plan.Checkpoint.Identity {
		for id, attrs := range previous.Resources {
			plan.Checkpoint.Resources[id] = attrs
		}
	}
	known := map[string]bool{}
	matched := false
	for _, r := range resources {
		known[r.ID] = true
	}
	reserved := map[string]bool{}
	for _, kind := range []string{"room", "zone", "scene", "behavior_instance"} {
		ids := []string{}
		for id := range remote[kind] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if known[id] || (opts.id != "" && opts.id != id) {
				continue
			}
			matched = true
			raw := remote[kind][id]
			var item hue.Group
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, err
			}
			name := pull.ResourceName(item.Metadata.Name)
			addr := opts.module + "hue_" + kind + "." + name
			path, pathErr := pull.NewResourcePath(destination, kind, name)
			if pull.AddressReserved(state, kind, name, scope) || reserved[addr] || pathErr != nil {
				// Batch import must accommodate repeated Hue scene names without
				// silently overwriting a declaration. UUIDs make the suffix stable.
				name += "_" + strings.ReplaceAll(id, "-", "_")
				addr = opts.module + "hue_" + kind + "." + name
				path, pathErr = pull.NewResourcePath(destination, kind, name)
			}
			if pathErr != nil || pull.AddressReserved(state, kind, name, scope) || reserved[addr] {
				plan.Problems = append(plan.Problems, addr+": destination is reserved or cannot be parsed")
				continue
			}
			reserved[addr] = true

			src, err := pull.RemoteDefinition(raw, kind, name, nil)
			if err != nil {
				plan.Problems = append(plan.Problems, addr+": "+err.Error())
				continue
			}
			attrs, err := pull.CanonicalAttributes(src)
			if err != nil {
				return nil, err
			}
			plan.Creates = append(plan.Creates, pullCreation{Address: addr, ID: id, Path: path, Source: src})
			plan.Checkpoint.Resources[id] = attrs
			plan.Targets = append(plan.Targets, addr)
		}
	}
	// Generate same-module group references after all new names are reserved.
	for i, c := range plan.Creates {
		kind, name, _ := pull.ResourceAddress(c.Address)
		if kind != "scene" {
			continue
		}
		groups := map[string]string{}
		for _, r := range resources {
			if (r.Kind == "room" || r.Kind == "zone") && pull.ModuleAddress(r.Address) == scope {
				_, n, _ := pull.ResourceAddress(r.Address)
				groups[r.ID] = "hue_" + r.Kind + "." + n
			}
		}
		for _, g := range plan.Creates {
			k, n, _ := pull.ResourceAddress(g.Address)
			if k == "room" || k == "zone" {
				groups[g.ID] = "hue_" + k + "." + n
			}
		}
		source, e := pull.RemoteDefinition(remote[kind][c.ID], kind, name, groups)
		if e != nil {
			return nil, e
		}
		plan.Creates[i].Source = source
	}
	referenceResources := append([]pull.ManagedResource(nil), resources...)
	for _, c := range plan.Creates {
		kind, _, _ := pull.ResourceAddress(c.Address)
		idJSON, _ := json.Marshal(c.ID)
		referenceResources = append(referenceResources, pull.ManagedResource{Address: c.Address, Kind: kind, ID: c.ID, Attributes: map[string]json.RawMessage{"id": idJSON}})
	}
	for _, r := range resources {
		known[r.ID] = true
		rscope := pull.ModuleAddress(r.Address)
		if opts.id != "" && opts.id != r.ID && opts.id != r.Address {
			continue
		}
		if opts.module != "" && rscope != scope && !strings.HasPrefix(rscope, scope+".") {
			continue
		}
		matched = true
		_, name, _ := pull.ResourceAddress(r.Address)
		dir := modules[rscope]
		if dir == "" {
			plan.Problems = append(plan.Problems, r.Address+": source module not found")
			continue
		}
		old := r.Attributes
		if checkpoint, ok := plan.Checkpoint.Resources[r.ID]; ok {
			old = checkpoint
		}
		raw := remote[r.Kind][r.ID]
		ctx := pull.StateContext(referenceResources, rscope)
		var change *pull.Change
		if raw == nil {
			change, err = pull.PrepareRemoval(dir, r.Kind, name, old, ctx)
			if err == nil {
				plan.Removes = append(plan.Removes, r)
				delete(plan.Checkpoint.Resources, r.ID)
			}
		} else {
			src, e := pull.RemoteDefinition(raw, r.Kind, name, nil)
			if e != nil {
				plan.Problems = append(plan.Problems, r.Address+": "+e.Error())
				continue
			}
			change, err = pull.PrepareSync(dir, r.Kind, name, old, src, ctx)
			if err == nil {
				attrs, e := pull.CanonicalAttributes(src)
				if e != nil {
					return nil, e
				}
				plan.Checkpoint.Resources[r.ID] = attrs
				plan.Targets = append(plan.Targets, r.Address)
			}
		}
		if err != nil {
			plan.Problems = append(plan.Problems, r.Address+": "+err.Error())
			continue
		}
		for i := range change.Edits {
			change.Edits[i].Field = r.Address + "." + change.Edits[i].Field
		}
		if len(change.Edits) > 0 {
			plan.Changes = append(plan.Changes, change)
		}
	}
	if opts.id != "" && !matched {
		plan.Problems = append(plan.Problems, "resource not found in selected scope: "+opts.id)
	}
	plan.Changes, err = pull.CombineChanges(plan.Changes)
	if err != nil {
		return nil, err
	}
	imports, err := pull.CheckRemovals(modules, plan.Changes, plan.Removes)
	if err != nil {
		plan.Problems = append(plan.Problems, err.Error())
	} else {
		plan.Changes = append(plan.Changes, imports...)
		plan.Changes, err = pull.CombineChanges(plan.Changes)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func pullBatch(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	opts, err := parsePullOptions(args, true)
	// In unified pull the optional positional argument selects an existing address
	// or UUID. Two positional arguments remain exclusive to legacy --new.
	if err != nil || opts.address != "" {
		return fmt.Errorf("usage: hue-tf pull [UUID | RESOURCE_ADDRESS] [--module NAME[.NAME...]] [--write]")
	}
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
	var checkpoint pullCheckpoint
	cache := []byte(nil)
	if cache, err = os.ReadFile(".hue-pull-baseline.json"); err == nil {
		if err = json.Unmarshal(cache, &checkpoint); err != nil {
			return fmt.Errorf("invalid pull baseline: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	client, err := deps.newClient(os.Getenv("HUE_BRIDGE_HOST"), key)
	if err != nil {
		return err
	}
	if checkpoint.Version != 0 && (checkpoint.Version != 1 || checkpoint.Identity != pullIdentity(state)) {
		return fmt.Errorf("pull baseline belongs to a different bridge, workspace or state; restore the matching environment, or archive the baseline and preview again")
	}
	remote, err := fetchPullResources(ctx, client)
	if err != nil {
		return err
	}
	plan, err := prepareBatch(state, remote, opts, checkpoint)
	if err != nil {
		return err
	}
	for _, c := range plan.Changes {
		for _, e := range c.Edits {
			fmt.Fprintf(out, "%s: %s: %s -> %s\n", c.Path, e.Field, e.Before, e.After)
		}
	}
	for _, c := range plan.Creates {
		fmt.Fprintf(out, "Import %s [%s] -> %s\n%s\n", c.Address, c.ID, c.Path, c.Source)
	}
	for _, r := range plan.Removes {
		fmt.Fprintf(out, "Forget %s [%s] (already absent from bridge)\n", r.Address, r.ID)
	}
	fmt.Fprintf(out, "Pull: %d new, %d changed files, %d deleted, %d blocked.\n", len(plan.Creates), len(plan.Changes), len(plan.Removes), len(plan.Problems))
	for _, problem := range plan.Problems {
		fmt.Fprintf(out, "Blocked: %s\n", problem)
	}
	if len(plan.Problems) > 0 {
		return fmt.Errorf("pull is blocked; no files or state were changed")
	}
	if !opts.write {
		fmt.Fprintln(out, "Preview only. Use --write to update configuration and Terraform state. The bridge is never changed.")
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
	currentCache, e := os.ReadFile(".hue-pull-baseline.json")
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if !bytes.Equal(cache, currentCache) {
		return fmt.Errorf("pull baseline changed during preview; retry")
	}
	return writeBatch(ctx, plan, state, out, deps)
}

func writeBatch(ctx context.Context, plan *batchPlan, state []byte, out io.Writer, deps dependencies) (err error) {
	run := deps.terraform
	if run == nil {
		run = terraformCommand
	}
	transaction, err := filepath.Abs(".hue-pull-transaction")
	if err != nil {
		return err
	}
	if err = os.Mkdir(transaction, 0700); err != nil {
		return err
	}
	success := false
	stateTouched := false
	var writtenChanges []*pull.Change
	var writtenCreates []pullCreation
	defer func() {
		if !success && !stateTouched {
			rollbackErr := rollbackPullFiles(writtenChanges, writtenCreates)
			if rollbackErr == nil {
				_ = os.RemoveAll(transaction)
				fmt.Fprintln(out, "Pull stopped before changing state; file edits were rolled back.")
				return
			}
			fmt.Fprintf(out, "File rollback needs attention: %v\n", rollbackErr)
		}
		if success {
			_ = os.RemoveAll(transaction)
		} else {
			fmt.Fprintf(out, "Pull interrupted. Recovery data: %s. Files/state may be partially updated; no bridge mutations were requested.\n", transaction)
		}
	}()

	if err = os.WriteFile(filepath.Join(transaction, "state-before.json"), state, 0600); err != nil {
		return err
	}
	// Backups and a manifest are durable before any file or state mutation.
	manifest := struct {
		Changed map[string]string
		Created []string
		Imports []pullCreation
		Removed []pull.ManagedResource
	}{Changed: map[string]string{}, Imports: plan.Creates, Removed: plan.Removes}
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
	for _, c := range plan.Creates {
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
	for _, c := range plan.Creates {
		if err = pull.WriteNew(c.Path, c.Source); err != nil {
			return err
		}
		writtenCreates = append(writtenCreates, c)
	}
	if _, err = run(ctx, "validate", "-no-color"); err != nil {
		return err
	}
	stateTouched = true
	if len(plan.Removes) > 0 {
		args := []string{"state", "rm"}
		for _, r := range plan.Removes {
			args = append(args, r.Address)
		}
		if _, err = run(ctx, args...); err != nil {
			return err
		}
	}
	for _, c := range plan.Creates {
		if _, err = run(ctx, "import", "-input=false", c.Address, c.ID); err != nil {
			return err
		}
	}
	if len(plan.Targets) > 0 {
		saved := filepath.Join(transaction, "refresh.tfplan")
		args := []string{"plan", "-refresh-only", "-input=false", "-out=" + saved}
		for _, addr := range plan.Targets {
			args = append(args, "-target="+addr)
		}
		if _, err = run(ctx, args...); err != nil {
			return err
		}
		data, e := run(ctx, "show", "-json", saved)
		if e != nil {
			return e
		}
		if err = checkRefreshPlan(data); err != nil {
			return err
		}
		if _, err = run(ctx, "apply", "-input=false", saved); err != nil {
			return err
		}
	}
	updated, err := stateReader(deps)(ctx)
	if err != nil {
		return err
	}
	plan.Checkpoint.Identity = pullIdentity(updated)
	data, err := json.MarshalIndent(plan.Checkpoint, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(transaction, "baseline.json")
	if err = os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	if err = os.Rename(temp, ".hue-pull-baseline.json"); err != nil {
		return err
	}
	success = true
	fmt.Fprintln(out, "Pulled configuration and Terraform state. The bridge was not changed.")
	return nil
}
func checkRefreshPlan(data []byte) error {
	var plan struct {
		FormatVersion   string `json:"format_version"`
		Errored         bool
		ResourceChanges []struct{ Change struct{ Actions []string } } `json:"resource_changes"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	if plan.FormatVersion == "" {
		return fmt.Errorf("invalid Terraform plan JSON")
	}
	if plan.Errored {
		return fmt.Errorf("refresh plan has errors")
	}
	for _, r := range plan.ResourceChanges {
		if len(r.Change.Actions) != 1 || r.Change.Actions[0] != "no-op" {
			return fmt.Errorf("refresh plan unexpectedly proposes a resource mutation")
		}
	}
	return nil
}

func rollbackPullFiles(changes []*pull.Change, creates []pullCreation) error {
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
