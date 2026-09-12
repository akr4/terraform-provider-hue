package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestPreviewCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	raw := `{"format_version":"1.2","planned_values":{"root_module":{"resources":[{"address":"hue_scene.s","type":"hue_scene","values":{"name":"Evening","actions":{}}}]}}}`
	var out bytes.Buffer
	deps := dependencies{stdin: strings.NewReader(raw), terraform: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("preview must not invoke Terraform")
		return nil, nil
	}}
	if e := runWith(context.Background(), []string{"preview", "--html", "--output", "preview.html"}, &out, &bytes.Buffer{}, deps); e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile("preview.html")
	if e != nil || !bytes.Contains(body, []byte("Evening")) {
		t.Fatalf("%s %v", body, e)
	}
	if e = previewCommand([]string{"--html", "--output", "preview.html"}, strings.NewReader("bad"), &out); e == nil {
		t.Fatal("bad input accepted")
	}
	after, _ := os.ReadFile("preview.html")
	if !bytes.Equal(after, body) {
		t.Fatal("bad input overwrote preview")
	}
	for _, args := range [][]string{{"--bad"}, {"--color", "purple"}, {"one", "two"}, {"--output"}} {
		if e = previewCommand(args, strings.NewReader(raw), &out); e == nil {
			t.Fatal(args)
		}
	}
}
