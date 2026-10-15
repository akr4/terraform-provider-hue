#!/bin/sh
# Checks the archives in dist/ before they are released. The Terraform Registry
# rejects a release that has a file the SHA256SUMS does not list, and reads
# every archive the SHA256SUMS lists as a provider package. The provider and
# hue-tf therefore have separate SHA256SUMS files and releases.
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
status=0

# check DIR PATTERN: DIR has one SHA256SUMS, which lists every archive in DIR
# with its hash and only files whose names match the awk regex PATTERN. It sets
# sums to the SHA256SUMS.
check() {
  dir=$1
  pattern=$2
  set -- "$repo_dir/$dir"/*_SHA256SUMS
  if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    echo "expected one SHA256SUMS file in $dir/" >&2
    exit 1
  fi
  sums=$1
  for file in "$repo_dir/$dir"/*.zip "$repo_dir/$dir"/*.tar.gz; do
    [ -f "$file" ] || continue
    if ! awk '{ print $2 }' "$sums" | grep -Fqx "${file##*/}"; then
      echo "${sums##*/} does not list ${file##*/}" >&2
      status=1
    fi
  done
  unexpected=$(pattern=$pattern awk '$2 !~ ENVIRON["pattern"] { print $2 }' "$sums")
  if [ -n "$unexpected" ]; then
    printf '%s lists files that do not match %s:\n%s\n' "${sums##*/}" "$pattern" "$unexpected" >&2
    status=1
  fi
  # The manifest is checksummed from its source file, so it is not in dist/.
  if ! (cd "$repo_dir/$dir" && grep -v '_manifest\.json$' "${sums##*/}" | shasum -a 256 -c --quiet -); then
    status=1
  fi
}

check dist/provider '^terraform-provider-hue_.*([.]zip|_manifest[.]json)$'
if ! grep -q '_manifest\.json$' "$sums"; then
  echo "${sums##*/} does not list the Registry manifest" >&2
  status=1
fi
check dist/cli '^hue-tf_.*([.]tar[.]gz|[.]zip)$'
exit "$status"
