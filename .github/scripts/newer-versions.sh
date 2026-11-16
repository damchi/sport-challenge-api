#!/usr/bin/env bash
# The version tags (v*) released after the commit $1: those of its descendants.
# Run in a clone that has the history and the tags. The tags on the commit
# itself are not newer (several can sit on the same commit).
set -euo pipefail
sha=$1
git tag --contains "$sha" --list 'v[0-9]*' | grep -vxFf <(git tag --points-at "$sha") || true
