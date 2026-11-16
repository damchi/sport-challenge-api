#!/usr/bin/env bash
# Tests of plan-deploy.sh on a throwaway repository: commit c1 (tagged v1.0.0),
# c2, c3 (tagged v1.1.0), c4. origin/main is moved to the commit under test.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
cd "$repo"
git init -q
git config user.email t@example.com
git config user.name t
for i in 1 2 3 4; do
  git commit -q --allow-empty -m "c$i"
  eval "c$i=$(git rev-parse HEAD)"
  case $i in 1) git tag v1.0.0 ;; 3) git tag v1.1.0 ;; esac
done
failed=0

# plan <tip> <event> <head> <ref> -> the output lines, "ERROR" on failure
plan() {
  git update-ref refs/remotes/origin/main "$1"
  EVENT=$2 HEAD_SHA=$3 REF=$4 "$here/plan-deploy.sh" 2>/dev/null | tr '\n' ' ' || echo ERROR
}

# The whole output must match (a field added or changed does not go unseen); an
# error is just "ERROR".
check() {
  local name=$1 want=$2 got=$3
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "tip, untagged: nothing, version since the last tag" \
  "sha=$c4 version=v1.1.0-1-g${c4:0:7} rollback=false production=false " \
  "$(plan "$c4" workflow_run "$c4" "")"
check "tip, tagged: production" \
  "sha=$c3 version=v1.1.0 rollback=false production=true " \
  "$(plan "$c3" workflow_run "$c3" "")"
check "tagged but no longer the tip: production still" \
  "sha=$c3 version=v1.1.0 rollback=false production=true " \
  "$(plan "$c4" workflow_run "$c3" "")"
check "untagged and no longer the tip: nothing" \
  "sha=$c2 version=v1.0.0-1-g${c2:0:7} rollback=false production=false " \
  "$(plan "$c4" workflow_run "$c2" "")"
check "rollback by tag" \
  "sha=$c1 version=v1.0.0 rollback=true production=true " \
  "$(plan "$c4" workflow_dispatch "" v1.0.0)"
check "rollback by sha" \
  "sha=$c2 version=v1.0.0-1-g${c2:0:7} rollback=true production=true " \
  "$(plan "$c4" workflow_dispatch "" "$c2")"
check "rollback to a commit that is not in main" "ERROR" \
  "$(plan "$c2" workflow_dispatch "" "$c4")"
check "rollback with a ref that looks like an option" "ERROR" \
  "$(plan "$c4" workflow_dispatch "" --all)"
check "rollback to an unknown ref" "ERROR" \
  "$(plan "$c4" workflow_dispatch "" nope)"

# A pre-release tag is no version: nothing goes to production.
git tag v1.2.0-rc1 "$c4"
check "Release on main, only a pre-release tag: nothing" \
  "sha=$c4 version=v1.1.0-1-g${c4:0:7} rollback=false production=false " \
  "$(plan "$c4" workflow_run "$c4" "")"
git tag -d v1.2.0-rc1 >/dev/null

# A repository without any tag: the version is made from the commit.
untagged=$(mktemp -d)
trap 'rm -rf "$repo" "$untagged"' EXIT
(
  cd "$untagged"
  git init -q
  git config user.email t@example.com
  git config user.name t
  git commit -q --allow-empty -m first
) 
u=$(git -C "$untagged" rev-parse HEAD)
cd "$untagged"
check "no tag at all: version made from the commit" \
  "sha=$u version=v0.0.0-${u:0:7} rollback=false production=false " \
  "$(plan "$u" workflow_run "$u" "")"

exit $failed
