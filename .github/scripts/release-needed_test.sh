#!/usr/bin/env bash
# Tests of release-needed.sh on a throwaway repository: a first commit tagged
# v1.0.0, then a documentation commit (doc), a code commit (code) and a commit
# mixing both (mixed).
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
cd "$repo"
git init -q
git config user.email t@example.com
git config user.name t
commit() {
  for file in "${@:2}"; do
    mkdir -p "$(dirname "$file")"
    echo "$1" >>"$file"
    git add "$file"
  done
  git commit -q -m "$1"
  git rev-parse HEAD
}
first=$(commit first go.mod)
git tag v1.0.0
doc=$(commit doc docs/release.md README.md)
code=$(commit code internal/x/x.go)
mixed=$(commit mixed docs/runbook.md cmd/api/main.go)
failed=0

check() {
  local name=$1 last=$2 sha=$3 want=$4 got
  got=$("$here/release-needed.sh" "$last" "$sha")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "documentation only since the last version" v1.0.0 "$doc" "release=false"
check "code since the last version"               v1.0.0 "$code" "release=true"
check "documentation and code mixed"              v1.0.0 "$mixed" "release=true"
check "no previous version"                       ""     "$first" "release=true"
exit $failed
