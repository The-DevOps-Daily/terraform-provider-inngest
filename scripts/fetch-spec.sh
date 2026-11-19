#!/bin/sh
# Download Inngest's published OpenAPI specs into testdata/spec/.
# The specs are not committed; see testdata/README.md.
set -eu
dir="$(cd "$(dirname "$0")/.." && pwd)/testdata/spec"
mkdir -p "$dir"
for v in v1 v2; do
  curl -fsSL --retry 3 -o "$dir/$v.json" "https://api-docs.inngest.com/api-specs/$v.json"
done
echo "Downloaded the specs to $dir"
