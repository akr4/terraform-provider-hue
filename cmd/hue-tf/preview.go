package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/akr4/terraform-provider-hue/internal/preview"
)

func previewCommand(args []string, in io.Reader, out io.Writer) error {
	usage := fmt.Errorf("usage: hue-tf preview [PLAN_JSON|-] [--html] [--all] [--color auto|always|never] [--inventory RESOURCE_JSON] [--output FILE]")
	html, all := false, false
	color := "auto"
	source, destination, inventory := "", "", ""
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--html", "--all":
			if seen[arg] {
				return usage
			}
			seen[arg] = true
			if arg == "--html" {
				html = true
			} else {
				all = true
			}
		case "--color", "--inventory", "--output":
			if seen[arg] || i+1 >= len(args) {
				return usage
			}
			seen[arg] = true
			i++
			switch arg {
			case "--color":
				color = args[i]
			case "--inventory":
				inventory = args[i]
			case "--output":
				destination = args[i]
			}
		default:
			if source != "" || (len(arg) > 1 && arg[0] == '-') {
				return usage
			}
			source = arg
		}
	}
	if color != "auto" && color != "always" && color != "never" {
		return usage
	}
	if source != "" && source != "-" {
		f, err := os.Open(source)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	if in == nil {
		in = os.Stdin
	}
	var names io.Reader
	if inventory != "" {
		f, err := os.Open(inventory)
		if err != nil {
			return err
		}
		defer f.Close()
		names = f
	}
	v, err := preview.Decode(in, names)
	if err != nil {
		return err
	}
	useColor := color == "always"
	if color == "auto" && destination == "" && os.Getenv("NO_COLOR") == "" {
		if f, ok := out.(*os.File); ok {
			if info, e := f.Stat(); e == nil {
				useColor = info.Mode()&os.ModeCharDevice != 0
			}
		}
	}
	render := func(w io.Writer) error {
		if html {
			return preview.HTML(w, v)
		}
		return preview.Terminal(w, v, all, useColor)
	}
	if destination == "" {
		return render(out)
	}
	// Build the complete file before replacing an older preview.
	f, err := os.CreateTemp(filepath.Dir(destination), ".hue-preview-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = render(f); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), destination)
}
