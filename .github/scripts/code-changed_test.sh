#!/usr/bin/env bash
# Tests of code-changed.sh on a throwaway repository: base, then a branch with
# documentation commits (doc, doc3), a code commit (code) and a rename of that
# code to docs/x.md (renamed), and main moved on with a code commit (moved)
# after the branch started.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
cd "$repo"
git init -q -b main
git config user.email t@example.com
git config user.name t
commit() {
  mkdir -p "$(dirname "$1")"
  echo "$2" >>"$1"
  git add "$1"
  git commit -q -m "$2"
  git rev-parse HEAD
}
base=$(commit go.mod base)
git switch -q -c branch
doc=$(commit docs/runbook.md doc)
commit README.md doc2 >/dev/null
doc3=$(commit .claude/skills/x/SKILL.md doc3)
code=$(commit internal/x/x.go code)
git mv internal/x/x.go docs/x.md
git commit -q -m renamed
renamed=$(git rev-parse HEAD)
git switch -q main
moved=$(commit cmd/api/main.go moved)
failed=0

check() {
  local name=$1 from=$2 to=$3 want=$4 got
  got=$("$here/code-changed.sh" "$from" "$to")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "docs/ only"                              "$base" "$doc" "code=false"
check "Markdown and .claude/ only"              "$base" "$doc3" "code=false"
check "a Go file"                               "$base" "$code" "code=true"
check "code renamed to docs/x.md"                "$code" "$renamed" "code=true"
check "base moved on: its changes do not count" "$moved" "$doc3" "code=false"
check "no change"                               "$base" "$base" "code=false"
check "missing base"                            "" "$doc" "code=true"
check "all-zero base (first push)"              "0000000000000000000000000000000000000000" "$doc" "code=true"
check "unknown base"                            "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" "$doc" "code=true"
exit $failed
