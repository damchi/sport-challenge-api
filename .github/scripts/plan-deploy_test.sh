#!/usr/bin/env bash
# Tests of plan-deploy.sh on a throwaway repository: commit c1 (tagged v1.0.0),
# c2, c3 (tagged v1.1.0), c4. origin/main and origin/staging are moved to the commits under test.
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

# plan <main tip> <staging tip> <event> <workflow> <branch> <head> <target> <ref>
# -> the output lines, "ERROR" on failure
plan() {
  git update-ref refs/remotes/origin/main "$1"
  git update-ref refs/remotes/origin/staging "$2"
  EVENT=$3 WORKFLOW=$4 BRANCH=$5 HEAD_SHA=$6 TARGET=$7 REF=$8 "$here/plan-deploy.sh" 2>/dev/null | tr '\n' ' ' || echo ERROR
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

check "CI green on the tip of staging: staging only" \
  "sha=$c4 version=v1.1.0-1-g${c4:0:7} rollback=false staging=true production=false " \
  "$(plan "$c3" "$c4" workflow_run CI staging "$c4" "" "")"
check "CI on staging, no longer the tip: nothing" \
  "sha=$c2 version=v1.0.0-1-g${c2:0:7} rollback=false staging=false production=false " \
  "$(plan "$c3" "$c4" workflow_run CI staging "$c2" "" "")"
check "CI on main: nothing (the release follows)" \
  "sha=$c3 version=v1.1.0 rollback=false staging=false production=false " \
  "$(plan "$c3" "$c4" workflow_run CI main "$c3" "" "")"
check "Release on main, tagged: production only" \
  "sha=$c3 version=v1.1.0 rollback=false staging=false production=true " \
  "$(plan "$c3" "$c4" workflow_run Release main "$c3" "" "")"
check "Release on main, tagged but no longer the tip: production still" \
  "sha=$c3 version=v1.1.0 rollback=false staging=false production=true " \
  "$(plan "$c4" "$c4" workflow_run Release main "$c3" "" "")"
check "Release on main, untagged: nothing" \
  "sha=$c4 version=v1.1.0-1-g${c4:0:7} rollback=false staging=false production=false " \
  "$(plan "$c4" "$c4" workflow_run Release main "$c4" "" "")"
check "Release on staging: nothing" \
  "sha=$c3 version=v1.1.0 rollback=false staging=false production=false " \
  "$(plan "$c3" "$c4" workflow_run Release staging "$c3" "" "")"
check "rollback to staging by tag" \
  "sha=$c1 version=v1.0.0 rollback=true staging=true production=false " \
  "$(plan "$c3" "$c4" workflow_dispatch "" "" "" staging v1.0.0)"
check "rollback to staging with a commit of staging only" \
  "sha=$c4 version=v1.1.0-1-g${c4:0:7} rollback=true staging=true production=false " \
  "$(plan "$c3" "$c4" workflow_dispatch "" "" "" staging "$c4")"
check "rollback to production by sha" \
  "sha=$c2 version=v1.0.0-1-g${c2:0:7} rollback=true staging=false production=true " \
  "$(plan "$c3" "$c4" workflow_dispatch "" "" "" production "$c2")"
check "rollback to production with a commit of staging only" "ERROR" \
  "$(plan "$c3" "$c4" workflow_dispatch "" "" "" production "$c4")"
check "rollback to a commit that is in neither branch" "ERROR" \
  "$(plan "$c2" "$c2" workflow_dispatch "" "" "" staging "$c4")"
check "rollback with a ref that looks like an option" "ERROR" \
  "$(plan "$c4" "$c4" workflow_dispatch "" "" "" staging --all)"
check "rollback to an unknown ref" "ERROR" \
  "$(plan "$c4" "$c4" workflow_dispatch "" "" "" staging nope)"

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
  "sha=$u version=v0.0.0-${u:0:7} rollback=false staging=true production=false " \
  "$(plan "$u" "$u" workflow_run CI staging "$u" "" "")"

exit $failed
