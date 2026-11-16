#!/usr/bin/env bash
# Tests of next-version.sh: each case gives the current version and the
# version expected.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
failed=0

check() {
  local name=$1 current=$2 want=$3 got
  got=$("$here/next-version.sh" "$current")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $name: got '$got', want '$want'"
    failed=1
  else
    echo "ok   $name"
  fi
}

check "first release"          ""        "v0.1.0"
check "the patch is raised"    "v1.2.3"  "v1.2.4"
check "major 0"                "v0.4.1"  "v0.4.2"
check "double-digit parts"     "v1.9.19" "v1.9.20"

if "$here/next-version.sh" "1.2.3" >/dev/null 2>&1; then
  echo "FAIL a version without v is refused"
  failed=1
else
  echo "ok   a version without v is refused"
fi

exit "$failed"
