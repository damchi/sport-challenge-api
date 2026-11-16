#!/usr/bin/env bash
# Prints the next version: every merge to main raises the patch, whatever the
# commit messages say.
#
#   next-version.sh [current]
#
# Without a current version (no tag yet), the first release is v0.1.0. A minor
# or major version is released by hand (see docs/release.md).
set -euo pipefail

current="${1:-}"
if [[ -z "$current" ]]; then
  echo "v0.1.0"
  exit 0
fi

if [[ ! "$current" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "next-version: '$current' is not a version of the form vMAJOR.MINOR.PATCH" >&2
  exit 1
fi
echo "v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((BASH_REMATCH[3] + 1))"
