// Package pull prepares narrow, reviewable edits to existing Terraform configuration.
package pull

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/akr4/terraform-provider-hue/internal/hue"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type Baseline struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Archetype string   `json:"archetype"`
	Children  []string `json:"children"`
	Group     string   `json:"group"`
	Actions   map[string]struct {
		Brightness *float64 `json:"brightness"`
		On         *bool    `json:"on"`
		Mirek      *int64   `json:"mirek"`
		Kelvin     *int64   `json:"kelvin"`
		XY         *hue.XY  `json:"color_xy"`
	} `json:"actions"`
}

// State resolves a full address while rejecting indexed instances and provider aliases.
func State(data []byte, address string) (Baseline, error) {
	var state struct {
		Resources []struct {
			Mode, Type, Name, Module, Provider string
			Instances                          []struct {
				IndexKey   json.RawMessage `json:"index_key"`
				Deposed    string
				Attributes Baseline
			}
		}
	}
	var result Baseline
	if err := json.Unmarshal(data, &state); err != nil {
		return result, fmt.Errorf("invalid Terraform state")
	}
	count := 0
	for _, r := range state.Resources {
		candidate := r.Type + "." + r.Name
		if r.Module != "" {
			candidate = r.Module + "." + candidate
		}
		if r.Mode != "managed" || candidate != address {
			continue
		}
		if r.Provider != `provider["registry.terraform.io/akr4/hue"]` {
			return result, fmt.Errorf("only the default akr4/hue provider is supported")
		}
		for _, i := range r.Instances {
			if len(i.IndexKey) > 0 || i.Deposed != "" {
				return result, fmt.Errorf("indexed or deposed instances are not supported")
			}
			result = i.Attributes
			count++
		}
	}
	if count != 1 || !regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`).MatchString(result.ID) {
		return result, fmt.Errorf("expected one imported resource at %s", address)
	}
	return result, nil
}

type Edit struct {
	Start, End    int
	Before, After string
	Light         string
	Field         string
}
type Change struct {
	Path              string
	Original, Updated []byte
	Edits             []Edit
}

func object(e hclsyntax.Expression) (map[string]hclsyntax.Expression, error) {
	o, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, fmt.Errorf("expected a literal object; expressions are not overwritten")
	}
	result := map[string]hclsyntax.Expression{}
	for _, item := range o.Items {
		key, d := item.KeyExpr.Value(nil)
		if d.HasErrors() || key.IsNull() || !key.IsKnown() || key.Type() != cty.String {
			return nil, fmt.Errorf("object keys must be literal strings")
		}
		k := key.AsString()
		if _, exists := result[k]; exists {
			return nil, fmt.Errorf("duplicate object key")
		}
		result[k] = item.ValueExpr
	}
	return result, nil
}

// Prepare patches existing action literals. Every other byte is retained.
func Prepare(dir, name string, baseline Baseline, scene hue.Scene) (*Change, error) {
	if scene.ID != baseline.ID || scene.Group.RID != baseline.Group {
		return nil, fmt.Errorf("scene identity/group differs from state")
	}
	change, block, err := findResource(dir, "scene", name)
	if err != nil {
		return nil, err
	}
	attr := block.Body.Attributes["actions"]
	if attr == nil {
		return nil, fmt.Errorf("actions must be a literal object")
	}
	actions, err := object(attr.Expr)
	if err != nil {
		return nil, err
	}
	if len(actions) != len(scene.Actions) || len(actions) != len(baseline.Actions) {
		return nil, fmt.Errorf("action membership changed; pull cannot add or remove actions")
	}
	seen := map[string]bool{}
	for _, remote := range scene.Actions {
		id := remote.Target.RID
		expr, ok := actions[id]
		old, known := baseline.Actions[id]
		if !ok || !known || seen[id] || remote.Target.RType != "light" {
			return nil, fmt.Errorf("action membership differs from state/configuration")
		}
		seen[id] = true
		fields, err := object(expr)
		if err != nil {
			return nil, err
		}
		number := func(p *float64) cty.Value {
			if p == nil {
				return cty.NilVal
			}
			return cty.NumberFloatVal(*p)
		}
		integer := func(p *int64) cty.Value {
			if p == nil {
				return cty.NilVal
			}
			return cty.NumberIntVal(*p)
		}
		boolean := func(p *bool) cty.Value {
			if p == nil {
				return cty.NilVal
			}
			return cty.BoolVal(*p)
		}
		var brightness *float64
		var on *bool
		var mirek, kelvin *int64
		if remote.Action.Dimming != nil {
			brightness = &remote.Action.Dimming.Brightness
			if *brightness < 0 || *brightness > 100 {
				return nil, fmt.Errorf("invalid bridge brightness")
			}
		}
		if remote.Action.On != nil {
			on = &remote.Action.On.On
		}
		if remote.Action.ColorTemperature != nil {
			mirek = &remote.Action.ColorTemperature.Mirek
			if *mirek <= 0 {
				return nil, fmt.Errorf("invalid bridge mirek")
			}
			k := color.MirekToKelvin(*mirek)
			kelvin = &k
		}
		for _, item := range []struct {
			key         string
			old, target cty.Value
		}{
			{"on", boolean(old.On), boolean(on)},
			{"brightness", number(old.Brightness), number(brightness)},
		} {
			if err := change.scalar(fields[item.key], item.old, item.target, id, item.key); err != nil {
				return nil, err
			}
		}
		switched, err := change.switchColor(expr, fields, old.Mirek, old.Kelvin, old.XY, remote.Action, id)
		if err != nil {
			return nil, err
		}
		if switched {
			continue
		}
		if fields["mirek"] != nil && fields["kelvin"] != nil {
			return nil, fmt.Errorf("%s: both mirek and kelvin are configured", id)
		}
		if fields["kelvin"] != nil {
			if err := change.scalar(fields["kelvin"], integer(old.Kelvin), integer(kelvin), id, "kelvin"); err != nil {
				return nil, err
			}
		} else {
			if err := change.scalar(fields["mirek"], integer(old.Mirek), integer(mirek), id, "mirek"); err != nil {
				return nil, err
			}
		}
		if fields["color_hex"] != nil {
			return nil, fmt.Errorf("%s: color_hex is not supported for pull because xy-to-hex is lossy; use color_xy", id)
		}
		xyExpr := fields["color_xy"]
		if xyExpr != nil || remote.Action.Color != nil {
			if xyExpr == nil || remote.Action.Color == nil {
				return nil, fmt.Errorf("%s: color_xy presence changed; not supported yet", id)
			}
			xy, err := object(xyExpr)
			if err != nil {
				return nil, err
			}
			if len(xy) != 2 || xy["x"] == nil || xy["y"] == nil {
				return nil, fmt.Errorf("color_xy must contain x and y")
			}
			for _, key := range []string{"x", "y"} {
				previous := cty.NilVal
				if old.XY != nil {
					v := old.XY.X
					if key == "y" {
						v = old.XY.Y
					}
					previous = cty.NumberFloatVal(v)
				}
				target := remote.Action.Color.XY.X
				if key == "y" {
					target = remote.Action.Color.XY.Y
				}
				if target < 0 || target > 1 {
					return nil, fmt.Errorf("invalid bridge color_xy")
				}
				if err := change.scalar(xy[key], previous, cty.NumberFloatVal(target), id, "color_xy."+key); err != nil {
					return nil, err
				}
			}
		}

	}
	change.finish()
	return change, nil
}

// Write keeps a private backup and refuses stale edits or symlink destinations.
func (c *Change) Write() (string, error) {
	if len(c.Edits) == 0 {
		return "", nil
	}
	info, err := os.Lstat(c.Path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination must be a regular file")
	}
	current, err := os.ReadFile(c.Path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, c.Original) {
		return "", fmt.Errorf("file changed since preview")
	}
	backup, err := os.CreateTemp(filepath.Dir(c.Path), ".hue-pull-backup-*")
	if err != nil {
		return "", err
	}
	backupName := backup.Name()
	_, err = backup.Write(c.Original)
	closeErr := backup.Close()
	if err != nil {
		return backupName, err
	}
	if closeErr != nil {
		return backupName, closeErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), ".hue-pull-*")
	if err != nil {
		return backupName, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(c.Updated); err != nil {
		tmp.Close()
		return backupName, err
	}
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return backupName, err
	}
	if err = tmp.Close(); err != nil {
		return backupName, err
	}
	return backupName, os.Rename(tmp.Name(), c.Path)
}

// scalar makes a three-way comparison without evaluating or replacing expressions.
func (c *Change) scalar(expr hclsyntax.Expression, old, target cty.Value, id, field string) error {
	if expr == nil && target == cty.NilVal {
		return nil
	}
	if expr == nil || target == cty.NilVal {
		return fmt.Errorf("%s: %s presence changed; not supported yet", id, field)
	}
	literal, ok := expr.(*hclsyntax.LiteralValueExpr)
	if !ok || literal.Val.IsNull() || !literal.Val.Type().Equals(target.Type()) {
		return fmt.Errorf("%s: %s must be a literal; left unchanged", id, field)
	}
	local := literal.Val
	if equalScalar(local, target, field) {
		return nil
	}
	if old == cty.NilVal || !equalScalar(local, old, field) {
		return fmt.Errorf("%s: local %s differs from state; resolve the local edit first", id, field)
	}
	var text string
	if target.Type() == cty.Bool {
		text = fmt.Sprint(target.True())
	} else {
		text = target.AsBigFloat().Text('f', -1)
	}
	r := expr.Range()
	c.Edits = append(c.Edits, Edit{Start: r.Start.Byte, End: r.End.Byte, Before: string(r.SliceBytes(c.Original)), After: text, Light: id, Field: field})
	return nil
}

// Match the provider's coordinate rounding and tolerance. Gamut conversion is
// unnecessary here: these are direct xy literals compared with saved scene xy.
func equalScalar(config, actual cty.Value, field string) bool {
	if strings.HasPrefix(field, "color_xy.") && config.Type() == cty.Number && actual.Type() == cty.Number {
		a, _ := config.AsBigFloat().Float64()
		b, _ := actual.AsBigFloat().Float64()
		return math.Abs(math.Round(a*1e4)/1e4-b) <= 0.001+1e-12
	}
	return config.RawEquals(actual)
}

func findResource(dir, kind, name string) (*Change, *hclsyntax.Block, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		n := entry.Name()
		if strings.HasSuffix(n, ".tf.json") || n == "override.tf" || strings.HasSuffix(n, "_override.tf") {
			return nil, nil, fmt.Errorf("JSON configuration and override files are not supported")
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, nil, err
	}
	var change *Change
	var block *hclsyntax.Block
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		file, d := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
		if d.HasErrors() {
			return nil, nil, fmt.Errorf("cannot parse %s", path)
		}
		for _, b := range file.Body.(*hclsyntax.Body).Blocks {
			if b.Type == "resource" && len(b.Labels) == 2 && b.Labels[0] == "hue_"+kind && b.Labels[1] == name {
				if block != nil {
					return nil, nil, fmt.Errorf("duplicate resource")
				}
				block = b
				change = &Change{Path: path, Original: src}
			}
		}
	}
	if block == nil {
		return nil, nil, fmt.Errorf("resource definition not found in root .tf files")
	}
	for _, key := range []string{"count", "for_each", "provider"} {
		if block.Body.Attributes[key] != nil {
			return nil, nil, fmt.Errorf("%s is not supported", key)
		}
	}

	return change, block, nil
}
