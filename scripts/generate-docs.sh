#!/bin/sh
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
output_dir=$(mktemp -d .tfplugindocs.XXXXXX)
trap 'rm -rf "$output_dir"' EXIT HUP INT TERM
go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate \
  --provider-name hue --rendered-website-dir "$output_dir"
mkdir -p docs/resources docs/data-sources
cp "$output_dir/index.md" docs/index.md
cp "$output_dir"/resources/*.md docs/resources/
cp "$output_dir"/data-sources/*.md docs/data-sources/
