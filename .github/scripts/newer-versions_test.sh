#!/usr/bin/env bash
# Tests of newer-versions.sh on a throwaway repository: c1 (tagged v1.0.0 and
# v1.0.1), c2, c3 (tagged v1.1.0).
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
cd "$repo"
git init -q
git config user.email t@example.com
git config user.name t
for i in 1 2 3; do
  git commit -q --allow-empty -m "c$i"
  eval "c$i=$(git rev-parse HEAD)"
  case $i in 1) git tag v1.0.0; git tag v1.0.1 ;; 3) git tag v1.1.0 ;; esac
done
failed=0

check() {
  local name=$1 sha=$2 want=$3 got
  got=$("$here/newer-versions.sh" "$sha" | tr '\n' ' ')
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "two tags on the commit: none is newer"      "$c3" ""
check "an older commit sees the later tags"        "$c2" "v1.1.0 "
check "tags on the commit itself are not newer"    "$c1" "v1.1.0 "
exit $failed
