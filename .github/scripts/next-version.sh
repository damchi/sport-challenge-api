#!/usr/bin/env bash
# Prints the next semantic version, or nothing when no commit calls for one.
#
#   next-version.sh [current]    commit messages on stdin, each ended by a NUL
#                                (git log --format=%B%x00)
#
# Conventional commits decide: "feat" raises the minor version, "fix" the
# patch, a "!" before the colon or a "BREAKING CHANGE:" line the major one.
# Anything else (docs, chore, ci, test, refactor, merges...) calls for no
# version. While the major version is 0, a breaking change raises the minor
# one. Without a current version (no tag yet), the first release is v0.1.0.
set -euo pipefail

current="${1:-}"
if [[ -n "$current" && ! "$current" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "next-version: '$current' is not a version of the form vMAJOR.MINOR.PATCH" >&2
  exit 1
fi

# 0: nothing, 1: patch, 2: minor, 3: major
level=0
raise() { ((level >= $1)) || level=$1; }

# The last entry counts even without its NUL.
while IFS= read -r -d '' message || [[ -n "$message" ]]; do
  # git log ends each entry with a newline after the NUL.
  while [[ "$message" == $'\n'* ]]; do message=${message#$'\n'}; done
  subject=${message%%$'\n'*}
  if [[ "$subject" =~ ^([a-z]+)(\([^\)]*\))?(!)?:\ [^[:space:]] ]]; then
    case "${BASH_REMATCH[1]}" in
      feat) raise 2 ;;
      fix) raise 1 ;;
    esac
    [[ -n "${BASH_REMATCH[3]}" ]] && raise 3
  fi
  # A here-string, not a pipe: with pipefail, grep -q closing the pipe early
  # would fail the test and lose a breaking change in a long message.
  if grep -Eq '^BREAKING[ -]CHANGE: ' <<<"$message"; then
    raise 3
  fi
done

((level > 0)) || exit 0

if [[ -z "$current" ]]; then
  echo "v0.1.0"
  exit 0
fi

# The loop above reused BASH_REMATCH: read the current version again.
[[ "$current" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]
major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
if ((level == 3 && major == 0)); then
  level=2
fi
case "$level" in
  3) echo "v$((major + 1)).0.0" ;;
  2) echo "v${major}.$((minor + 1)).0" ;;
  1) echo "v${major}.${minor}.$((patch + 1))" ;;
esac
