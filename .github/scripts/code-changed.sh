#!/usr/bin/env bash
# Prints code=true when the changes from $1 to $2 touch anything but the
# documentation (docs/, Markdown files, .claude/), code=false otherwise. The
# changes are those of $2 since its merge base with $1, so a base that moved on
# does not count. A missing, all-zero or unknown base counts as code: when in
# doubt, the CI runs. Renames count as a deletion and an addition, so code
# moved under docs/ or renamed to .md still counts. Run in a clone that has
# both commits.
set -euo pipefail
base=${1:-}
head=${2:-}
if [[ -z "$base" || "$base" =~ ^0+$ ]] || ! files=$(git diff --name-only --no-renames "$base...$head" 2>/dev/null); then
  echo "code=true"
  exit 0
fi
while IFS= read -r file; do
  case "$file" in
    "" | docs/* | *.md | .claude/*) ;;
    *)
      echo "code=true"
      exit 0
      ;;
  esac
done <<<"$files"
echo "code=false"
