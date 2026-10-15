package pull

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

type configFile struct {
	path string
	body hcl.Body
}

// configFiles parses a directory's configuration the way Terraform loads it:
// native and JSON files, with override files after the primary files.
// import-blocks only reads these files, so no syntax is refused.
func configFiles(dir string) ([]configFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var primary, overrides []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || (!strings.HasSuffix(n, ".tf") && !strings.HasSuffix(n, ".tf.json")) {
			continue
		}
		// Terraform ignores editor backup and swap files.
		if strings.HasPrefix(n, ".") || strings.HasSuffix(n, "~") || (strings.HasPrefix(n, "#") && strings.HasSuffix(n, "#")) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(n, ".json"), ".tf")
		if base == "override" || strings.HasSuffix(base, "_override") {
			overrides = append(overrides, n)
		} else {
			primary = append(primary, n)
		}
	}
	parser := hclparse.NewParser()
	var files []configFile
	for _, n := range append(primary, overrides...) {
		path := filepath.Join(dir, n)
		var f *hcl.File
		var d hcl.Diagnostics
		if strings.HasSuffix(n, ".json") {
			f, d = parser.ParseJSONFile(path)
		} else {
			f, d = parser.ParseHCLFile(path)
		}
		if d.HasErrors() {
			return nil, fmt.Errorf("cannot parse %s", path)
		}
		files = append(files, configFile{path: path, body: f.Body})
	}
	return files, nil
}

// configBlocks returns the blocks of one type from a directory's configuration.
func configBlocks(dir string, header hcl.BlockHeaderSchema) ([]*hcl.Block, []string, error) {
	files, err := configFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	var blocks []*hcl.Block
	var paths []string
	for _, f := range files {
		content, _, d := f.body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{header}})
		if d.HasErrors() {
			return nil, nil, fmt.Errorf("cannot read %s blocks in %s", header.Type, f.path)
		}
		for _, b := range content.Blocks {
			blocks = append(blocks, b)
			paths = append(paths, f.path)
		}
	}
	return blocks, paths, nil
}

// blockAttributes returns the named attributes of a block that are present.
func blockAttributes(b *hcl.Block, names ...string) (hcl.Attributes, error) {
	schema := &hcl.BodySchema{}
	for _, n := range names {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: n})
	}
	content, _, d := b.Body.PartialContent(schema)
	if d.HasErrors() {
		return nil, fmt.Errorf("cannot read %s block at %s", b.Type, b.DefRange)
	}
	return content.Attributes, nil
}

// literalString returns a string written as a constant. References, function
// calls and other expressions are not evaluated.
func literalString(expr hcl.Expression) (string, bool) {
	v, d := expr.Value(nil)
	if d.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

// Definitions returns TYPE.NAME for every resource declared in a directory.
func Definitions(dir string) (map[string]bool, error) {
	blocks, _, err := configBlocks(dir, hcl.BlockHeaderSchema{Type: "resource", LabelNames: []string{"type", "name"}})
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, b := range blocks {
		result[b.Labels[0]+"."+b.Labels[1]] = true
	}
	return result, nil
}
