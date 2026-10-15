#!/bin/sh
# Checks a snapshot in dist/: the Terraform Registry rejects a release that has
# a file the SHA256SUMS does not list.
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir/dist"
set -- *_SHA256SUMS
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
  echo "expected one SHA256SUMS file in dist/" >&2
  exit 1
fi
sums=$1
status=0
for file in *.zip *.tar.gz; do
  [ -f "$file" ] || continue
  if ! grep -q "  $file\$" "$sums"; then
    echo "$sums does not list $file" >&2
    status=1
  fi
done
if ! grep -q '_manifest\.json$' "$sums"; then
  echo "$sums does not list the registry manifest" >&2
  status=1
fi
# The manifest is checksummed from its source file, so only archives are here.
grep -v '_manifest\.json$' "$sums" | shasum -a 256 -c - >/dev/null
exit "$status"
