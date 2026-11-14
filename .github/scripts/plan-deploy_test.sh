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

# plan <tip> <event> <head> <target> <ref> -> the output lines, "ERROR" on failure
plan() {
  git update-ref refs/remotes/origin/main "$1"
  EVENT=$2 HEAD_SHA=$3 TARGET=$4 REF=$5 "$here/plan-deploy.sh" 2>/dev/null | tr '\n' ' ' || echo ERROR
}

check() {
  local name=$1 want=$2 got=$3
  if [[ "$got" != *"$want"* ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "tip, untagged: staging only, version since the last tag" \
  "version=v1.1.0-1-g${c4:0:7} rollback=false staging=true production=false superseded=false" \
  "$(plan "$c4" workflow_run "$c4" "" "")"
check "tip, tagged: staging then production" \
  "sha=$c3 version=v1.1.0 rollback=false staging=true production=true superseded=false" \
  "$(plan "$c3" workflow_run "$c3" "" "")"
check "tagged but no longer the tip: production only" \
  "sha=$c3 version=v1.1.0 rollback=false staging=false production=true superseded=true" \
  "$(plan "$c4" workflow_run "$c3" "" "")"
check "untagged and no longer the tip: nothing" \
  "staging=false production=false superseded=true" \
  "$(plan "$c4" workflow_run "$c2" "" "")"
check "rollback to staging by tag" \
  "sha=$c1 version=v1.0.0 rollback=true staging=true production=false superseded=false" \
  "$(plan "$c4" workflow_dispatch "" staging v1.0.0)"
check "rollback to production by sha" \
  "sha=$c2 version=v1.0.0-1-g${c2:0:7} rollback=true staging=false production=true" \
  "$(plan "$c4" workflow_dispatch "" production "$c2")"
check "rollback to a commit that is not in main" "ERROR" \
  "$(plan "$c2" workflow_dispatch "" staging "$c4")"
check "rollback with a ref that looks like an option" "ERROR" \
  "$(plan "$c4" workflow_dispatch "" staging --all)"
check "rollback to an unknown ref" "ERROR" \
  "$(plan "$c4" workflow_dispatch "" staging nope)"

exit $failed
