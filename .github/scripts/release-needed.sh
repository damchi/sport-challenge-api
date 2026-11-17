#!/usr/bin/env bash
# Whether the commit $2 gets a new version (release.yml), given $1, the last
# version tag before it (empty when there is none). Prints release=true or
# release=false: a commit that only changes the documentation since the last
# version (code-changed.sh) gets none, so it is not deployed either
# (plan-deploy.sh deploys a tagged commit only). Without a previous version, or
# when in doubt, the release goes on. Run in a clone that has the history and
# the tags.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
last=${1:-}
sha=$2
if [ -n "$last" ] && [ "$("$here/code-changed.sh" "$last" "$sha")" = "code=false" ]; then
  echo "release=false"
else
  echo "release=true"
fi
